/*
 * MinIO Cloud Storage, (C) 2016, 2017 MinIO, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// lockedRandSource provides protected rand source, implements rand.Source interface.
type lockedRandSource struct {
	lk  sync.Mutex
	src rand.Source
}

// Int63 returns a non-negative pseudo-random 63-bit integer as an
// int64.
func (r *lockedRandSource) Int63() (n int64) {
	r.lk.Lock()
	n = r.src.Int63()
	r.lk.Unlock()
	return
}

// Seed uses the provided seed value to initialize the generator to a
// deterministic state.
func (r *lockedRandSource) Seed(seed int64) {
	r.lk.Lock()
	r.src.Seed(seed)
	r.lk.Unlock()
}

// MaxJitter will randomize over the full exponential backoff time
const MaxJitter = 1.0

// NoJitter disables the use of jitter for randomizing the
// exponential backoff time
const NoJitter = 0.0

// Global random source for fetching random values.
var globalRandomSource = rand.New(&lockedRandSource{
	src: rand.NewSource(UTCNow().UnixNano()),
})

// newRetryTimerJitter creates a timer with exponentially increasing delays
// until the maximum retry attempts are reached. - this function is a fully
// configurable version, meant for only advanced use cases. For the most part
// one should use newRetryTimerSimple and newRetryTimer.
func newRetryTimerWithJitter(ctx context.Context, unit time.Duration, cap time.Duration, jitter float64) <-chan int {
	attemptCh := make(chan int)

	// normalize jitter to the range [0, 1.0]
	if jitter < NoJitter {
		jitter = NoJitter
	}
	if jitter > MaxJitter {
		jitter = MaxJitter
	}

	// computes the exponential backoff duration according to
	// https://www.awsarchitectureblog.com/2015/03/backoff.html
	exponentialBackoffWait := func(attempt int) time.Duration {
		// 1<<uint(attempt) below could overflow, so limit the value of attempt
		maxAttempt := 30
		if attempt > maxAttempt {
			attempt = maxAttempt
		}
		//sleep = random_between(0, min(cap, base * 2 ** attempt))
		sleep := unit * time.Duration(1<<uint(attempt))
		if sleep > cap {
			sleep = cap
		}
		if jitter != NoJitter {
			sleep -= time.Duration(globalRandomSource.Float64() * float64(sleep) * jitter)
		}
		return sleep
	}

	go func() {
		defer close(attemptCh)
		nextBackoff := 0
		// Channel used to signal after the expiry of backoff wait seconds.
		var timer *time.Timer
		for {
			select { // Attempts starts.
			case attemptCh <- nextBackoff:
				nextBackoff++
			case <-ctx.Done():
				// Stop the routine.
				return
			}
			timer = time.NewTimer(exponentialBackoffWait(nextBackoff))
			// wait till next backoff time or till doneCh gets a message.
			select {
			case <-timer.C:
			case <-ctx.Done():
				// stop the timer and return.
				timer.Stop()
				return
			}

		}
	}()

	// Start reading..
	return attemptCh
}

// Default retry constants.
const (
	defaultRetryUnit = time.Second      // 1 second.
	defaultRetryCap  = 30 * time.Second // 30 seconds.
)

// newRetryTimerSimple creates a timer with exponentially increasing delays
// until the maximum retry attempts are reached. - this function is a
// simpler version with all default values.
func newRetryTimerSimple(ctx context.Context) <-chan int {
	return newRetryTimerWithJitter(ctx, defaultRetryUnit, defaultRetryCap, MaxJitter)
}
