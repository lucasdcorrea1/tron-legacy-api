package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"
)

// lokiCfg holds the Grafana Cloud Loki push credentials. Nil means disabled.
type lokiCfg struct {
	pushURL string
	user    string
	apiKey  string
}

var (
	lokiSink  *lokiCfg
	lokiLines chan []byte
)

// EnableLokiSink turns on best-effort forwarding of every log line written
// through Writer() to a Grafana Cloud Loki instance, in addition to stdout.
// No-op if any argument is empty (default: disabled, zero overhead).
func EnableLokiSink(pushURL, user, apiKey string) {
	if pushURL == "" || user == "" || apiKey == "" {
		return
	}
	lokiSink = &lokiCfg{pushURL: pushURL, user: user, apiKey: apiKey}
	lokiLines = make(chan []byte, 1000)
	go lokiBatcher()
}

// Writer returns the io.Writer used for all system logging (slog and the
// standard log package). It always writes to stdout first — that never
// changes — and, if EnableLokiSink was called, also mirrors the line to the
// Loki sink without ever blocking the caller.
func Writer() *lokiWriter {
	return &lokiWriter{}
}

type lokiWriter struct{}

func (lokiWriter) Write(p []byte) (int, error) {
	n, err := os.Stdout.Write(p)
	if lokiLines != nil {
		line := make([]byte, len(p))
		copy(line, p)
		select {
		case lokiLines <- line:
		default:
			// buffer full: drop rather than block request handling
		}
	}
	return n, err
}

func lokiBatcher() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}
	batch := make([][]byte, 0, 100)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		pushToLoki(client, batch)
		batch = batch[:0]
	}

	for {
		select {
		case line := <-lokiLines:
			batch = append(batch, line)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

type lokiPushBody struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

func pushToLoki(client *http.Client, lines [][]byte) {
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	values := make([][2]string, 0, len(lines))
	for _, l := range lines {
		values = append(values, [2]string{now, string(l)})
	}

	body := lokiPushBody{Streams: []lokiStream{{
		Stream: map[string]string{"job": "tron-legacy-api", "env": "production"},
		Values: values,
	}}}

	raw, err := json.Marshal(body)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, lokiSink.pushURL, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(lokiSink.user, lokiSink.apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
