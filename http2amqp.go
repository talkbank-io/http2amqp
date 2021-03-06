// Copyright (c) 2015 by Doug Watson. MIT permissive licence - Contact info at http://github.com/dougwatson/http2amqp
package main

//usage:
// POST http://localhost:8080/QUEUE_NAME
// put the message in the body
import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/streadway/amqp"
	"log"
)

type ResponseQueue struct {
	path        string
	body        string
	queryParams string
}

func responseQueueToString(queue ResponseQueue) string {
	return fmt.Sprintf("%s/%s/%s", queue.path, queue.body, queue.queryParams)
}

var uri, httpPort, logFilePath, configFile string
var logFile *os.File
var queueMap = make(map[string]string)
var mutex = &sync.Mutex{}

func init() {
	flag.StringVar(&uri, "uri", "amqp://guest:guest@localhost:5672/TEST", "The address for the amqp server (including vhost)")
	flag.StringVar(&httpPort, "httpPort", "8090", "The listen port for the https GET requests")
	flag.StringVar(&logFilePath, "logFilePath", "/var/log/http2amqp/http2amqp.log", "Set path to get log information")
	flag.StringVar(&configFile, "configFile", "./config.json", "Get config file with arguments")
}

func parseConfig(uri *string, httpPort *string, logFilePath *string) {
	config, err := ioutil.ReadFile(configFile)
	dat := make(map[string]string)

	if err != nil {
		fmt.Printf("Config file error: %v\n", err)
		os.Exit(1)
	}
	byt := []byte(config)
	error := json.Unmarshal(byt, &dat)
	if error != nil {
		fmt.Printf("Config file error: %v\n", error)
		os.Exit(1)
	}

	*uri = dat["uri"]
	*httpPort = dat["httpPort"]
	*logFilePath = dat["logFilePath"]
}

func main() {
	flag.Parse()
	fmt.Print(configFile)
	_, error := os.OpenFile(configFile, os.O_RDWR, 0666)
	if error != nil {
		// File not exists and must be create
		config, _ := os.Create(configFile)
		jsonData := map[string]string{"uri": uri, "httpPort": httpPort, "logFilePath": logFilePath}
		strJson, _ := json.Marshal(jsonData)
		config.WriteString(string(strJson))
		fmt.Print(string(strJson))

		defer config.Close()

	}
	parseConfig(&uri, &httpPort, &logFilePath)
	fmt.Printf("\nParsed params:\n uri=%s\n httpPort=%s\n logFilePath=%s\n", uri, httpPort, logFilePath)

	logFile, err := os.OpenFile(logFilePath, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		fmt.Printf("Log error: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	myWriter := bufio.NewWriter(logFile)
	fmt.Fprintf(myWriter, "%v startup uri=%s\n", time.Now(), uri)
	log.Printf("%v startup uri=%s\n", time.Now(), uri)
	myWriter.Flush()
	queueResponses, lines := writeRabbit(uri, myWriter) //read device requests rabbitmq o
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		staticHandler(w, r, queueResponses, lines)
	})
	http.ListenAndServe(":"+httpPort, nil) //address= ":8080"
}

func staticHandler(w http.ResponseWriter, req *http.Request, responses chan ResponseQueue, lines chan string) {
	result := ""
	select {
	case responses <- parseRequest(req):
	case <-time.After(time.Second):
		result = "NETWORK_SEND_TIMEOUT|503"
		log.Printf("webReply function will be run as defer: %s\n", result)
		webReply(result, w)
		return
	}
	select {
	case result = <-lines:
	case <-time.After(time.Second):
		result = "NETWORK_REC_TIMEOUT|504"
	}
	log.Printf("webReply function will be run as defer: %s\n", result)
	webReply(result, w)
}
func webReply(result string, w http.ResponseWriter) {
	var statusMessage string
	var statusCode int
	resultArr := strings.Split(result, "|") //split into 2 parts- status message and status code
	if len(resultArr) == 2 {
		statusMessage = resultArr[0]
		statusCode, _ = strconv.Atoi(resultArr[1])
	} else {
		statusMessage, statusCode = "PARSE_ERROR", 510
	}
	w.WriteHeader(statusCode)
	fmt.Fprintf(w, statusMessage)
	log.Printf(statusMessage, w)
}
func parseRequest(req *http.Request) ResponseQueue {
	urlPath := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:] //take everything after the http://localhost:8080/ (it gets the queue name)
	bodyBytes, _ := ioutil.ReadAll(req.Body)
	queryParams := req.URL.RawQuery
	body := string(bodyBytes[:])
	log.Printf("Original=%s, Url path=%s, body=%s\n ", req.URL.Path, urlPath, body)
	return ResponseQueue{urlPath, body, queryParams}
}

func writeRabbit(amqpURI string, myWriter *bufio.Writer) (chan ResponseQueue, chan string) {
	responseQueues := make(chan ResponseQueue)
	lines := make(chan string)

	go func() {
		connectionAttempts := 0
		for {
			conn, err1 := amqp.Dial(amqpURI)
			if err1 != nil {
				fmt.Printf("%v err1=%v\n", time.Now(), err1)
				log.Printf("%v err1=%v\n", time.Now(), err1)
				fmt.Fprintf(myWriter, "%v err1=%v\n", time.Now(), err1)
				log.Printf("%v err1=%v\n", time.Now(), err1)
				time.Sleep(time.Second)
				myWriter.Flush()
				continue
			}
			channel, err2 := conn.Channel()
			if err2 != nil {
				fmt.Fprintf(myWriter, "%v err2=%v\n", time.Now(), err2)
				log.Printf("%v err2=%v\n", time.Now(), err2)
			}
			i := 0
			go func() {
				fmt.Fprintf(myWriter, "%v %d %d closing (will reopen): %s\n", time.Now(), connectionAttempts, i, <-conn.NotifyClose(make(chan *amqp.Error)))
				log.Printf("%v %d %d closing (will reopen): %s\n", time.Now(), connectionAttempts, i, <-conn.NotifyClose(make(chan *amqp.Error)))
			}()

			myWriter.Flush()
			result := ""
			for {
				i++
				responseQueue := <-responseQueues

				startTime := time.Now()

				log.Printf("URLpath=%s, line=%s\n ", responseQueue.path, responseQueueToString(responseQueue))
				if len(responseQueue.path) < 2 {
					fmt.Fprintf(myWriter, "%v %d %d Skip this message b/c it is missing a QUEUE name on the URL or a message body. count=%d line=%v\n", time.Now(), connectionAttempts, i, len(responseQueue.path), responseQueueToString(responseQueue))
					log.Printf("%v %d %d Skip this message b/c it is missing a QUEUE name on the URL or a message body. count=%d line=%v\n", time.Now(), connectionAttempts, i, len(responseQueue.path), responseQueueToString(responseQueue))
					myWriter.Flush()
					lines <- "skip"
					continue
				}

				if queueMap[responseQueue.path] == "" {
					//if we have never seen this queue name work before
					_, err := channel.QueueInspect(responseQueue.path)
					if err == nil {
						mutex.Lock()
						queueMap[responseQueue.path] = "1" //save the successful lookup of the queue name
						mutex.Unlock()
					} else {
						result = "BAD_QUEUE_NAME|400"
						lines <- result
						fmt.Fprintf(myWriter, "%v %d %d %s/%db %s %.6f\n", time.Now(), connectionAttempts, i, responseQueue.path, len(responseQueue.body), result, 1.0)
						log.Printf("%v %d %d %s/%db %s %.6f\n", time.Now(), connectionAttempts, i, responseQueue.body, len(responseQueue.body), result, 1.0)
						break
					}

				}

				err3 := channel.Publish(
					"",                 //exchange
					responseQueue.path, //routingKey, for some reason we need to put the queue name here
					false,              //mandatory - don't quietly drop messages in case of missing Queue
					false,              //immediate
					amqp.Publishing{
						Headers:         amqp.Table{"query_string": responseQueue.queryParams},
						ContentType:     "text/plain",
						ContentEncoding: "UTF-8",
						Body:            []byte(responseQueue.body),
						DeliveryMode:    amqp.Persistent, // 1=non-persistent(Transient), 2=persistent
						Priority:        9,
					},
				)
				if err3 != nil {
					result = "NETWORK_ERROR|502"
					lines <- result
					fmt.Fprintf(logFile, "%v %d %d \nerr3 saw a network error=%v\n", time.Now(), connectionAttempts, i, err3)
					log.Printf("%v %d %d \nerr3 saw a network error=%v\n", time.Now(), connectionAttempts, i, err3)
					myWriter.Flush()
					//TODO - we should put the message back onto the channel since the publish failed
					break //probably the connection broke due to a network issue, so break out of this loop so it will re-connect
				}

				result = "OK|200" // message OK for VK.com
				lines <- result
				duration := (time.Since(startTime)).Seconds()
				fmt.Fprintf(myWriter, "%v %d %d %s/%db %s %.6f\n", time.Now(), connectionAttempts, i, responseQueue.path, len(responseQueue.body), result, duration)
				log.Printf("%v %d %d %s/%db %s %.6f\n", time.Now(), connectionAttempts, i, responseQueue.path, len(responseQueue.body), result, duration)
				if result != "OK|200" {
					break
				}

				myWriter.Flush()
			}

		}
	}()

	return responseQueues, lines
}
