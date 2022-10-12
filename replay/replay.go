package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	spb "github.com/openconfig/gribi/v1/proto/service"
	lpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
)

type timeseries map[time.Time][]*spb.ModifyRequest

func fromFile(fn string) ([]*lpb.GrpcLogEntry, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, fmt.Errorf("cannot read file %s, error: %v", err)
	}

	msgs := []*lpb.GrpcLogEntry{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		p := &lpb.GrpcLogEntry{}
		if err := prototext.Unmarshal(s.Bytes(), p); err != nil {
			return nil, fmt.Errorf("invalid entry in log, message `%s` could not be converted to GrpcLogEntry, %v", s.Bytes(), err)
		}
		msgs = append(msgs, p)
	}
	return msgs, nil
}

func clientTimeseries(pb []*lpb.GrpcLogEntry, timeQuantum time.Duration) (timeseries, error) {
	ts := timeseries{}
	timeBucket := time.Unix(0, 0)
	for _, p := range pb {
		if p.Type != lpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE {
			continue
		}

		eventTime := time.Unix(p.Timestamp.Seconds, int64(p.Timestamp.Nanos))
		fmt.Printf("timedelta is %s\n", eventTime.Sub(timeBucket))
		if eventTime.Sub(timeBucket) > timeQuantum {
			timeBucket = eventTime
			fmt.Printf("** rewrite timeBucket to %s\n", timeBucket)
		}

		m := &spb.ModifyRequest{}
		if err := proto.Unmarshal(p.GetMessage().GetData(), m); err != nil {
			return nil, fmt.Errorf("cannot unmarshal ModifyRequest %s, %v", p, err)
		}
		ts[timeBucket] = append(ts[timeBucket], m)
		fmt.Printf("%d.%d: %s\n", p.Timestamp.Seconds, p.Timestamp.Nanos, m)
	}
	return ts, nil
}

type event struct {
	sleep  time.Duration
	events []*spb.ModifyRequest
}

func replaySchedule(ts timeseries) ([]*event, error) {
	k := []time.Time{}
	for t := range ts {
		k = append(k, t)
	}
	sort.Slice(k, func(i, j int) bool { return k[i].Before(k[j]) })
	prev := k[0]
	sched := []*event{}
	for _, q := range k {
		sched = append(sched, &event{
			sleep:  q.Sub(prev),
			events: ts[q],
		})
		prev = q
	}
	return sched, nil
}

func main() {
	msgs, err := fromFile("testdata/5ms-2ops.txtpb")
	if err != nil {
		log.Exitf("cannot read messages, %v", err)
	}
	_ = msgs
	fmt.Printf("%d\n", len(msgs))
	ts, err := clientTimeseries(msgs, 5*time.Millisecond)
	if err != nil {
		log.Exitf("cannot build timeseries, %v", err)
	}
	for t, ms := range ts {
		fmt.Printf("%s --> %v\n", t, ms)
	}

	sched, err := replaySchedule(ts)
	if err != nil {
		log.Exitf("cannot form schedule, %v", err)
	}

	for _, e := range sched {
		time.Sleep(e.sleep)
		fmt.Printf("sleep %s.......\n", e.sleep)
		fmt.Printf("	send %v\n", e.events)
	}
}
