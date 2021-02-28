/*
MIT License

Copyright (c) 2021 Duc Truong

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// retryFunc is the function signature for a function which is retryable. The
// stop bool indicates whether or not the retry should be halted indicating a
// terminal error. The error return can accompany either a true or false stop
// return to provide context when needed.
type retryFunc func(ctx context.Context) (stop bool, err error)

// retry will retry the passed function f until any of the following conditions
// are met:
//  - the function returns stop=true and err=nil
//  - the retryAttempts limit is reached
//  - the context is cancelled
func retry(ctx context.Context, retryInterval time.Duration, retryAttempts int, f retryFunc) error {

	var (
		retryCount int
		lastErr    error
	)

	for {

		if ctx.Err() != nil {
			if lastErr != nil {
				return fmt.Errorf("retry failed with %v; last error: %v", ctx.Err(), lastErr)
			}
			return ctx.Err()
		}

		stop, err := f(ctx)
		if stop {
			return err
		}

		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			lastErr = err
		}

		if err == nil {
			return nil
		}

		retryCount++

		if retryCount == retryAttempts {
			return errors.New("reached retry limit")
		}
		time.Sleep(retryInterval)
	}
}
