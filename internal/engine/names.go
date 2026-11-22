package engine

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
	"github.com/JacobJNilsson/simple-factory/internal/task"
)

// roundSession returns the name of a round session, as review-t4-2 for the reviewer of round 2 of task t4.
func roundSession(prefix, id string, round int) string {
	return fmt.Sprintf("%s-%s-%d", prefix, id, round)
}

// sessionID is what a session name of a worker or a reviewer tells: the kind, the owning agent, the task and, for a round session, the round.
type sessionID struct {
	kind  kind
	owner string
	task  string
	round int
}

// taskPart matches the part of a session name after the prefix: the task id, then the round for a round session.
var taskPart = regexp.MustCompile(`^(t[0-9]+)(?:-([0-9]+))?$`)

// owners returns the claude agents that own a session, sorted, with each task session before each round session.
func (s *sessions) owners() []process.Agent {
	var task, round []process.Agent
	for _, name := range slices.Sorted(maps.Keys(s.proc.Agents)) {
		a := s.proc.Agents[name]
		switch {
		case a.Harness != process.Claude || a.Like != "":
		case a.Session == process.SessionRound:
			round = append(round, a)
		default:
			task = append(task, a)
		}
	}
	return append(task, round...)
}

// parse reads a session name of a worker or a reviewer against the session prefixes of the process.
func (s *sessions) parse(name string) (sessionID, bool) {
	for _, o := range s.owners() {
		rest, ok := strings.CutPrefix(name, o.SessionName+"-")
		m := taskPart.FindStringSubmatch(rest)
		if !ok || m == nil || (m[2] != "") != (o.Session == process.SessionRound) {
			continue
		}
		round, _ := strconv.Atoi(m[2])
		return sessionID{kind: s.kindOfOwner(o.Name), owner: o.Name, task: m[1], round: round}, true
	}
	return sessionID{}, false
}

// kindOf reads the kind from a session name. Only the empty name is the operator. The old name of the lead reads as the lead.
func (e *Engine) kindOf(agent string) (kind, bool) {
	switch {
	case agent == "":
		return operator, true
	case protocol.AgentName(agent) == protocol.Lead:
		return lead, true
	}
	if id, ok := e.sess.parse(agent); ok {
		return id.kind, true
	}
	return operator, false
}

// workerOf returns the session name of the worker of a task.
func (e *Engine) workerOf(t task.Task) string { return e.sess.workerOf(t) }

// reviewerOf returns the session name of the reviewer of the task's current round.
func (e *Engine) reviewerOf(t task.Task) string { return e.sess.reviewerOf(t) }

// allSessions returns every session that a task may have had: each task session, then each round session of rounds 1 to t.Round.
func (s *sessions) allSessions(t task.Task) []string {
	var names []string
	for _, o := range s.owners() {
		if o.Session != process.SessionRound {
			names = append(names, o.SessionName+"-"+t.ID)
			continue
		}
		for r := 1; r <= t.Round; r++ {
			names = append(names, roundSession(o.SessionName, t.ID, r))
		}
	}
	return names
}
