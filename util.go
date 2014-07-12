package main

import "sort"

type servicesByPriority []*Service

func (s servicesByPriority) Len() int           { return len(s) }
func (s servicesByPriority) Less(i, j int) bool { return s[i].Config.Priority < s[j].Config.Priority }
func (s servicesByPriority) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s servicesByPriority) Sort()              { sort.Stable(s) }

type quit struct {
	stop    chan bool
	stopped chan bool
}

func newQuit() *quit {
	return &quit{
		stop:    make(chan bool, 1),
		stopped: make(chan bool, 1),
	}
}

func (q *quit) sendStop() {
	select {
	case q.stop <- true:
	default:
	}
}

func (q *quit) sendStopped() {
	select {
	case q.stopped <- true:
	default:
	}
}

func (q *quit) waitForStop() {
	<-q.stop
}

func (q *quit) waitForStopped() {
	<-q.stopped
}
