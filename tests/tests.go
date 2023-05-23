package tests

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func mustPass(t *testing.T, err error) {
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}

func waitFor[T any](f func() (T, error)) (T, error) {
	resultChan := make(chan T, 1)
	errorChan := make(chan error, 1)

	go func(resultChan chan<- T, errorChan chan<- error) {
		after := time.After(time.Minute)
		var lastError error

		for {
			select {
			case <-after:
				if lastError != nil {
					errorChan <- lastError
				} else {
					errorChan <- errors.New("timed out waiting for secret")
				}

				return
			default:
			}

			result, err := f()
			if err != nil {
				lastError = err
				time.Sleep(time.Second)
			} else {
				resultChan <- result
				return
			}
		}
	}(resultChan, errorChan)

	select {
	case res := <-resultChan:
		return res, nil
	case err := <-errorChan:
		var empty T
		return empty, err
	}
}
