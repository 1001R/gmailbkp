package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

var authResponse string = "HTTP/1.1 200 OK\r\n" +
	"Content-Length: 46\r\n" +
	"Content-Type: text/plain\r\n" +
	"Connection: close\r\n\r\n" +
	"  __      _\n" +
	"o'')}____//\n" +
	" `_/      )\n" +
	" (_(_/-(_/"

var errInvalidAuthResponse = fmt.Errorf("invalid authorization response")

var DONE = struct{}{}

type GmailApiMessage struct {
	Id           string   `json:"id"`
	ThreadId     string   `json:"threadId"`
	LabelIds     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	HistoryId    string   `json:"historyId"`
	InternalDate string   `json:"internalDate"`
	SizeEstimate int      `json:"sizeEstimate"`
	Raw          string   `json:"raw"`
}

type GmailApiMessageList struct {
	Messages           []*GmailApiMessage `json:"messages"`
	NextPageToken      string             `json:"nextPageToken"`
	ResultSizeEstimate int                `json:"resultSizeEstimate"`
}

type GmailApiUserProfile struct {
	EmailAddress  string `json:"emailAddress"`
	MessagesTotal int    `json:"messagesTotal"`
	ThreadsTotal  int    `json:"threadsTotal"`
	HistoryId     string `json:"historyId"`
}

const NUM_WORKERS = 8

func getMessageCount(token string) (int, error) {
	profileURL := "https://gmail.googleapis.com/gmail/v1/users/me/profile"
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	var profile GmailApiUserProfile
	err := httpRequest(http.MethodGet, profileURL, headers, nil, &profile)
	if err != nil {
		return 0, err
	}
	return profile.MessagesTotal, nil
}

func collectMessages(token string, messgeIdChannel chan *GmailApiMessage) {
	msgurl := "https://gmail.googleapis.com/gmail/v1/users/me/messages"
	var pageToken string
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	for {
		params := url.Values{}
		params.Add("includeSpamTrash", "false")
		params.Add("maxResults", "500")
		if pageToken != "" {
			params.Add("pageToken", pageToken)
		}
		var messageList GmailApiMessageList
		err := httpRequest(http.MethodGet, msgurl+"?"+params.Encode(), headers, nil, &messageList)
		if err != nil {
			log.Fatal(err)
		}
		for _, m := range messageList.Messages {
			messgeIdChannel <- m
			time.Sleep(20 * time.Millisecond)
		}
		if messageList.NextPageToken == "" {
			break
		}
		pageToken = messageList.NextPageToken
	}
	close(messgeIdChannel)
}

func downloadMessages(worker int, token string, messageIdChannel chan *GmailApiMessage, messageChannel chan *GmailApiMessage) {
	apiURL := "https://gmail.googleapis.com/gmail/v1/users/me/messages/"
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	for m := range messageIdChannel {
		for i := 0; i < 5; i++ {
			if err := httpRequest(http.MethodGet, apiURL+m.Id+"?format=RAW", headers, nil, m); err == nil {
				if i > 0 {
					fmt.Printf("Message %v, worker #%v, %v retries\n", m.Id, worker, i)
				}
				break
			} else {
				log.Printf("Cannot download message %v: %v", m.Id, err)
				time.Sleep(100 * time.Millisecond)
			}
		}
		messageChannel <- m
	}
	messageChannel <- nil
}

func closeWriter(w io.WriteCloser) {
	if err := w.Close(); err != nil {
		log.Printf("Error while closing writer: %v", err)
	}
}

func flushWriter(w *bufio.Writer) {
	if err := w.Flush(); err != nil {
		log.Printf("Error while flushing writer: %v", err)
	}
}

func processMessage(messageChannel chan *GmailApiMessage, doneChannel chan any, totalMessageCount int) {
	defer func() {
		doneChannel <- DONE
	}()
	numClosed := 0
	mboxFile, err := os.Create("messages.mbox.gz")
	if err != nil {
		log.Fatal(err)
	}
	defer closeWriter(mboxFile)

	mboxBufWriter := bufio.NewWriter(mboxFile)
	defer flushWriter(mboxBufWriter)

	mboxWriter := gzip.NewWriter(mboxFile)
	defer closeWriter(mboxWriter)

	messageCount := 0
	start := time.Now()
	for m := range messageChannel {
		if m == nil {
			numClosed++
			if numClosed == NUM_WORKERS {
				break
			}
			continue
		}
		internalDateInt, err := strconv.ParseInt(m.InternalDate, 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		internalDate := time.UnixMilli(internalDateInt)
		rawBody, err := base64.URLEncoding.DecodeString(m.Raw)
		if err != nil {
			log.Fatal(err)
		}
		bodyReader := bytes.NewReader(rawBody)
		bodyScanner := bufio.NewScanner(bodyReader)
		bodyScanner.Split(bufio.ScanLines)
		isHeader := true
		if messageCount > 0 {
			mboxWriter.Write([]byte{10})
		}
		mboxWriter.Write([]byte(fmt.Sprintf("From - %v\n", internalDate.Format(time.ANSIC))))
		for bodyScanner.Scan() {
			line := bodyScanner.Text()
			if isHeader {
				if line == "" {
					isHeader = false
				}
			} else {
				rxFrom := regexp.MustCompile(`^>*From`)
				if rxFrom.MatchString(line) {
					line = ">" + line
				}
			}
			if _, err := mboxWriter.Write([]byte(line + "\n")); err != nil {
				log.Fatal(err)
			}
		}
		mboxWriter.Write([]byte{10})
		messageCount++
		if messageCount%500 == 0 {
			fmt.Printf("%v messages written => %v%%\n", messageCount, messageCount*100/totalMessageCount)
		}
	}
	end := time.Now()
	fmt.Printf("%v messages in %v\n", messageCount, end.Sub(start))
}

func main() {
	auth, err := NewAuthenticator()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(auth.URL())
	token, err := auth.Authenticate()
	if err != nil {
		log.Fatal(err)
	}
	messageCount, err := getMessageCount(token)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Total number of messages: %v\n", messageCount)

	messageIdChannel := make(chan *GmailApiMessage)
	messageChannel := make(chan *GmailApiMessage, 100)
	doneChannel := make(chan any)

	for i := 0; i < NUM_WORKERS; i++ {
		go downloadMessages(i, token, messageIdChannel, messageChannel)
	}
	go processMessage(messageChannel, doneChannel, messageCount)

	collectMessages(token, messageIdChannel)

	<-doneChannel
}
