package bluetooth

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter for BLE commands
type RateLimiter struct {
	mu            sync.Mutex
	config        *RateLimitConfig
	tokens        int
	lastRefill    time.Time
	commandDelay  time.Duration
	lastCommand   time.Time
}

// NewRateLimiter creates a new rate limiter with the given configuration
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}
	
	return &RateLimiter{
		config:       config,
		tokens:       config.BurstSize,
		lastRefill:   time.Now(),
		commandDelay: time.Second / time.Duration(config.MaxCommandsPerSecond),
		lastCommand:  time.Time{},
	}
}

// Allow checks if a command can be sent now, considering rate limits
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	now := time.Now()
	
	// Refill tokens based on elapsed time
	rl.refillTokens(now)
	
	// Check if we have tokens available
	if rl.tokens <= 0 {
		return false
	}
	
	// Check if enough time has passed since last command
	if !rl.lastCommand.IsZero() && now.Sub(rl.lastCommand) < rl.commandDelay {
		return false
	}
	
	// Consume a token and update last command time
	rl.tokens--
	rl.lastCommand = now
	
	return true
}

// Wait blocks until a command can be sent, respecting rate limits
func (rl *RateLimiter) Wait() {
	for !rl.Allow() {
		// Sleep for a short duration before checking again
		time.Sleep(10 * time.Millisecond)
	}
}

// WaitWithTimeout blocks until a command can be sent or timeout is reached
func (rl *RateLimiter) WaitWithTimeout(timeout time.Duration) bool {
	start := time.Now()
	
	for !rl.Allow() {
		if time.Since(start) >= timeout {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	
	return true
}

// refillTokens adds tokens to the bucket based on elapsed time
// Must be called with mutex locked
func (rl *RateLimiter) refillTokens(now time.Time) {
	elapsed := now.Sub(rl.lastRefill)
	if elapsed <= 0 {
		return
	}
	
	// Calculate how many tokens to add based on elapsed time
	tokensToAdd := int(elapsed.Seconds() * float64(rl.config.MaxCommandsPerSecond))
	
	if tokensToAdd > 0 {
		rl.tokens += tokensToAdd
		if rl.tokens > rl.config.BurstSize {
			rl.tokens = rl.config.BurstSize
		}
		rl.lastRefill = now
	}
}

// GetStats returns current rate limiter statistics
func (rl *RateLimiter) GetStats() (tokens int, lastCommand time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	rl.refillTokens(time.Now())
	return rl.tokens, rl.lastCommand
}

// Reset resets the rate limiter to its initial state
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	rl.tokens = rl.config.BurstSize
	rl.lastRefill = time.Now()
	rl.lastCommand = time.Time{}
}

// UpdateConfig updates the rate limiting configuration
func (rl *RateLimiter) UpdateConfig(config *RateLimitConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	if config == nil {
		return
	}
	
	rl.config = config
	rl.commandDelay = time.Second / time.Duration(config.MaxCommandsPerSecond)
	
	// Adjust tokens if burst size changed
	if rl.tokens > config.BurstSize {
		rl.tokens = config.BurstSize
	}
}

// CommandQueue manages a queue of commands with rate limiting
type CommandQueue struct {
	mu          sync.RWMutex
	queue       []*Command
	rateLimiter *RateLimiter
	stopChan    chan struct{}
	isRunning   bool
	sendFunc    func(*Command) error
}

// NewCommandQueue creates a new command queue with rate limiting
func NewCommandQueue(config *RateLimitConfig, sendFunc func(*Command) error) *CommandQueue {
	return &CommandQueue{
		queue:       make([]*Command, 0),
		rateLimiter: NewRateLimiter(config),
		stopChan:    make(chan struct{}),
		sendFunc:    sendFunc,
	}
}

// Start starts the command queue processor
func (cq *CommandQueue) Start() {
	cq.mu.Lock()
	if cq.isRunning {
		cq.mu.Unlock()
		return
	}
	cq.isRunning = true
	cq.mu.Unlock()
	
	go cq.processQueue()
}

// Stop stops the command queue processor
func (cq *CommandQueue) Stop() {
	cq.mu.Lock()
	if !cq.isRunning {
		cq.mu.Unlock()
		return
	}
	cq.isRunning = false
	cq.mu.Unlock()
	
	close(cq.stopChan)
}

// Enqueue adds a command to the queue
func (cq *CommandQueue) Enqueue(cmd *Command) {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	
	cmd.Timestamp = time.Now()
	cq.queue = append(cq.queue, cmd)
}

// EnqueuePriority adds a command to the front of the queue (high priority)
func (cq *CommandQueue) EnqueuePriority(cmd *Command) {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	
	cmd.Timestamp = time.Now()
	// Insert at the beginning
	cq.queue = append([]*Command{cmd}, cq.queue...)
}

// processQueue processes commands from the queue with rate limiting
func (cq *CommandQueue) processQueue() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-cq.stopChan:
			return
		case <-ticker.C:
			cq.processNextCommand()
		}
	}
}

// processNextCommand processes the next command in the queue if rate limits allow
func (cq *CommandQueue) processNextCommand() {
	cq.mu.Lock()
	
	// Check if queue is empty
	if len(cq.queue) == 0 {
		cq.mu.Unlock()
		return
	}
	
	// Check rate limits
	if !cq.rateLimiter.Allow() {
		cq.mu.Unlock()
		return
	}
	
	// Get the next command
	cmd := cq.queue[0]
	cq.queue = cq.queue[1:]
	
	cq.mu.Unlock()
	
	// Send the command
	if err := cq.sendFunc(cmd); err != nil {
		// Handle retry logic
		cmd.Retries++
		if cmd.Retries < 3 { // Max 3 retries
			// Re-queue the command for retry
			cq.mu.Lock()
			cq.queue = append([]*Command{cmd}, cq.queue...)
			cq.mu.Unlock()
		}
	}
}

// GetQueueLength returns the current queue length
func (cq *CommandQueue) GetQueueLength() int {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	
	return len(cq.queue)
}

// ClearQueue removes all commands from the queue
func (cq *CommandQueue) ClearQueue() {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	
	cq.queue = cq.queue[:0]
}

// GetOldestCommand returns the timestamp of the oldest command in the queue
func (cq *CommandQueue) GetOldestCommand() *time.Time {
	cq.mu.RLock()
	defer cq.mu.RUnlock()
	
	if len(cq.queue) == 0 {
		return nil
	}
	
	return &cq.queue[0].Timestamp
}

// RemoveExpiredCommands removes commands that are older than the specified duration
func (cq *CommandQueue) RemoveExpiredCommands(maxAge time.Duration) int {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	
	now := time.Now()
	removed := 0
	
	// Filter out expired commands
	newQueue := make([]*Command, 0, len(cq.queue))
	for _, cmd := range cq.queue {
		if now.Sub(cmd.Timestamp) <= maxAge {
			newQueue = append(newQueue, cmd)
		} else {
			removed++
		}
	}
	
	cq.queue = newQueue
	return removed
}