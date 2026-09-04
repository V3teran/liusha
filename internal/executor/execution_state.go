package executor

import (
	"sync"
)

// executionState 是执行协程和监察协程之间的共享状态。
type executionState struct {
	mu               sync.RWMutex
	steps            []Step
	totalTokens      int
	goal             string
	correctionChan   chan string
	shouldStop       bool
	stoppedByMonitor bool
}

func (s *executionState) addStep(step Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *executionState) updateStep(index int, step Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= 0 && index < len(s.steps) {
		s.steps[index] = step
	}
}

func (s *executionState) getRecentSteps(n int) []Step {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.steps) == 0 {
		return nil
	}

	start := len(s.steps) - n
	if start < 0 {
		start = 0
	}

	// 返回副本
	recent := make([]Step, len(s.steps)-start)
	copy(recent, s.steps[start:])
	return recent
}

func (s *executionState) getCurrentStep() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.steps)
}

func (s *executionState) addTokens(tokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalTokens += tokens
}

func (s *executionState) getTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalTokens
}

func (s *executionState) stop(byMonitor bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shouldStop = true
	s.stoppedByMonitor = byMonitor
}

func (s *executionState) isStopped() (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shouldStop, s.stoppedByMonitor
}
