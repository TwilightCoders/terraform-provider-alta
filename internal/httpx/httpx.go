// Package httpx holds the one HTTP round-trip helper shared by the cloud clients.
package httpx

import (
	"fmt"
	"io"
	"net/http"
)

// Do sends req and returns the status code and fully read body, always closing it.
func Do(client *http.Client, req *http.Request) (status int, body []byte, err error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing response body: %w", cerr)
		}
	}()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response body: %w", err)
	}
	return resp.StatusCode, body, nil
}
