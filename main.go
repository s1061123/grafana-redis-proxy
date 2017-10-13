package main

/*
grafana-redis-proxy
*/

import (
	"fmt"
	"net/http"
	"log"
	"flag"
	"io"
	"os"
	"encoding/json"
	"strconv"
	"time"
	"sort"
	"math"
	"strings"
	"github.com/garyburd/redigo/redis"
	"github.com/koron/go-dproxy"
)

// global variables, used at option
var redisHost *string
var debugEnable bool

// cache for grafana-redis value map.
var grafanaItemMap map[string]GrafanaItem
var grafanaItemList []string

// GrafanaItem is the cache structure to store Redis kvs.
// Some redis key contains multiple value in one value. 
// GrafanaItem make them separate it from value in redis.
type GrafanaItem struct {
	redisKey string
	index int
}

// serchHandler just send reply for '/search' request
func searchHandler(w http.ResponseWriter, r *http.Request) {
	resultJson, _ := json.Marshal(grafanaItemList)
	fmt.Fprintf(w, string(resultJson))
}


// initKeyList initializes grafanaItemMap and grafanaItemList at booting.
func initKeyList() (err error) {
	c, err := redis.Dial("tcp", *redisHost)
	if err != nil {
		return fmt.Errorf("Failed to connect redis (%s)", *redisHost)
	}
	defer c.Close()
	
	// write_redis puts items in collectd/values, so get the list.
	res, err := redis.Strings(c.Do("smembers", "collectd/values"))
	if err !=  nil  || len(res) == 0 {
		return fmt.Errorf("Failed to get collectd/values from redis (%s)", *redisHost)
		//fmt.Fprintf(os.Stderr, "err: %v", err)
	} 

	sort.Strings(res)
	// put prefix, 'collectd/' to all values..
	for i, v := range res {
		res[i] = "collectd/" + v
	}

	// get a value from each key.
	for _, v := range res {
		if err := c.Send("zrange", v, 0, 0); err != nil {
			fmt.Fprintf(os.Stderr, "err: %v", err)
		}
	}
	c.Flush()

	grafanaItemMap = map[string]GrafanaItem{}
	for _, v := range res {
		s, _ := redis.Strings(c.Receive())
		// count ':' to get the value.
		fields := strings.Count(s[0], ":")
		for i:= 0; i < fields; i++ {
			item := GrafanaItem{}
			item.index = i
			item.redisKey = v
			grafanaName := v
			// if redis has multiple values in one kvs entry,
			// identify as '<fieldname>#<index>'.
			if fields > 1 {
				grafanaName = fmt.Sprintf("%s#%d", v, i)
			}
			grafanaItemMap[grafanaName] = item
		}
	}

	// from grafanaItemMap, make grafanaItemList.
	grafanaItemList = make([]string, len(grafanaItemMap))
	i := 0
	for v := range grafanaItemMap {
		grafanaItemList[i] = v
		i++
	}
	sort.Strings(grafanaItemList)
	return
}

// defaultHandler just reply "OK" for grafana's keepalive.
func defaultHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

// getTargetFromReq retrieves 'targets' from grafana's request.
func getTargetFromReq(m map[string]interface{}) (s []string, err error) {
	targets, _ := dproxy.New(m).M("targets").Array()
	s = make([]string, len(targets))

	for i, v := range targets {
		s[i], _ = dproxy.New(v).M("target").String()
		
	}

	return s, nil
}

// getTimeFromReq retrieves time field (from or to) from grafana's request.
func getTimeFromReq(m map[string]interface{}, f string) (t time.Time, err error){
	var time_string string
	
	time_string, err = dproxy.New(m).M("range").M(f).String()
	if err != nil {
		err = fmt.Errorf("cast failed")
		return t, err
	}
	
	return time.Parse(time.RFC3339, time_string)
}

// convertRedisToGrafana parses redis data and convert it to native float value
// and time value, formatted integer (milliseconds).
func convertRedisToGrafana (redisData []string, index int) ([]float32, []int64) {
	ar_data := make([]float32, len(redisData))
	ar_time := make([]int64, len(redisData))

	for i, v := range redisData {
		v_ar := strings.Split(v, ":")
		if t, err := strconv.ParseFloat(v_ar[index+1], 32); err != nil {
			fmt.Printf("ERR: %v\n", err)
			ar_data[i] = 0
		} else {
			ar_data[i] = float32(t)
		}

		fval, _ := strconv.ParseFloat(v_ar[0], 64)
		ar_time[i] = int64(fval * 1000.0)
	}
	return ar_data, ar_time
}

// timeserie defines data structure that grafana's json plugin needs.
// this structure will be parsed into json at getOutputJson().
type timeserie struct {
	Target string `json:"target"`
	Datapoints []float32 `json:"datapoints"`
	Time []int64 `json:"datapoints"`
}

// getOutputJson reads data structure array and make json string by hands.
func getOutputJson (data []timeserie) (ans string) {
	ans = "["
	for _, v := range data {
		ans += "{"
		ans += fmt.Sprintf("\"target\":\"%s\",", v.Target)
		ans += "\"datapoints\":["
		for i, _ := range v.Datapoints {
			if math.IsNaN(float64(v.Datapoints[i])) {
				v.Datapoints[i] = 0
			}
			ans += fmt.Sprintf("[%.5f, %d],", v.Datapoints[i], v.Time[i])

		}
		ans = strings.TrimRight(ans, ",")
		ans += "]"
		ans += "},"
	}
	ans = strings.TrimRight(ans, ",")
	ans += "]"
	return
}

// getRedisVal sends query to redis and get the value, then returns json string used
// for grafana's REST reply.
func getRedisVal (host string, targetName []string, from string, to string, maxdatapoints int) (ans string) {
	c, _ := redis.Dial("tcp", host)
	defer c.Close()

	for _, v := range targetName {
		if debugEnable {
			fmt.Printf("zrangebyscore %s %s %s\n", grafanaItemMap[v].redisKey, from, to)
		}
		if err := c.Send("zrangebyscore", grafanaItemMap[v].redisKey, from, to); err != nil {
			fmt.Fprintf(os.Stderr, "err: %v", err)
		}
	}
	c.Flush()
	targetResult := make([]timeserie, len(targetName))
	for i1, v1 := range targetName {
		result := timeserie{}
		result.Target = v1
		redisResult, _ := redis.Strings(c.Receive())
		result.Datapoints, result.Time = convertRedisToGrafana(redisResult, grafanaItemMap[v1].index)
		targetResult[i1] = result
		//fmt.Printf("dp: %v\n", result.Datapoints)
	}

	ans = getOutputJson(targetResult)
	if debugEnable {
		fmt.Printf("json: %s\n", ans)
	}
	return 
}

// queryHandler handles '/query' request from grafana.
// it gets parameter from API request, get the data from redis then
// reply json data to grafana.
func queryHandler(w http.ResponseWriter, r *http.Request) {

	if r.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	//To allocate slice for request body
	length, err := strconv.Atoi(r.Header.Get("Content-Length"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	//Read body data to parse json
	body := make([]byte, length)
	length, err = r.Body.Read(body)
	if err != nil && err != io.EOF {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	//parse json
	var jsonBody map[string]interface{}
	err = json.Unmarshal(body[:length], &jsonBody)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var time_from,time_to time.Time
	if time_from, err = getTimeFromReq(jsonBody, "from"); err != nil{
		fmt.Printf("ERR: %v\n", err)
		w.WriteHeader(http.StatusBadRequest)		
		return 
	}

	if time_to, err = getTimeFromReq(jsonBody, "to"); err != nil{
		fmt.Printf("ERR: %v\n", err)
		w.WriteHeader(http.StatusBadRequest)		
		return 
	}

	var targets []string
	if targets, err = getTargetFromReq(jsonBody); err != nil {
		fmt.Printf("ERR: %v\n", err)
	}

	jsonOut := getRedisVal(*redisHost,
		targets,
		strconv.FormatInt(time_from.Unix(), 10),
		strconv.FormatInt(time_to.Unix(), 10),
		int(jsonBody["maxDataPoints"].(float64)))

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, jsonOut)
	return
}

// main function....
func main() {
	port := flag.Int("port", 8080, "Port for http server")
	redisHost = flag.String("redis", "localhost:6379", "Host for redis server")
	flag.BoolVar(&debugEnable, "debug", false, "Print verbose message")
	flag.Parse()

	if err := initKeyList(); err != nil {
		log.Fatal("Error at initKeyList(): ", err)
	}

	http.HandleFunc("/", defaultHandler)
	http.HandleFunc("/search", searchHandler)
	http.HandleFunc("/query", queryHandler)
	fmt.Printf("http server ready...\n")
	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
