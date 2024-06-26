package tests

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func mustPass(t *testing.T, err error) {
	if !assert.NoError(t, err) {
		t.FailNow()
	}
}

func waitForResult[T any](t *testing.T, f func() (T, error)) T {
	resultChan := make(chan T, 1)
	errorChan := make(chan error, 1)

	go func(resultChan chan<- T, errorChan chan<- error) {
		after := time.After(2 * time.Minute)
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
		return res
	case err := <-errorChan:
		assert.FailNow(t, err.Error())
		var value T
		return value
	}
}

func waitFor(f func() error) error {
	resultChan := make(chan struct{}, 1)
	errorChan := make(chan error, 1)

	go func(resultChan chan<- struct{}, errorChan chan<- error) {
		after := time.After(2 * time.Minute)
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

			err := f()
			if err != nil {
				lastError = err
				time.Sleep(time.Second)
			} else {
				resultChan <- struct{}{}
				return
			}
		}
	}(resultChan, errorChan)

	select {
	case <-resultChan:
		return nil
	case err := <-errorChan:
		return err
	}
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
