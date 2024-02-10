package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func httpRequest(method, url string, headers map[string]string, body io.Reader, result any) error {
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	for key, value := range headers {
		request.Header.Add(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("http status code %v", response.StatusCode)
	}
	if err != nil {
		return err
	}
	bodyBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(bodyBytes, result); err != nil {
		return err
	}
	return nil
}
