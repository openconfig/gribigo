package replay

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	log "github.com/golang/glog"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	spb "github.com/openconfig/gribi/v1/proto/service"
	"github.com/openconfig/gribigo/fluent"
	lpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
)

type timeseries map[time.Time][]*spb.ModifyRequest

func FromFile(fn string) ([]*lpb.GrpcLogEntry, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, fmt.Errorf("cannot read file %s, error: %v", fn, err)
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

func Timeseries(pb []*lpb.GrpcLogEntry, timeQuantum time.Duration) (timeseries, error) {
	ts := timeseries{}
	timeBucket := time.Unix(0, 0)
	for _, p := range pb {
		if p.Type != lpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE {
			continue
		}

		eventTime := time.Unix(p.Timestamp.Seconds, int64(p.Timestamp.Nanos))
		if eventTime.Sub(timeBucket) > timeQuantum {
			timeBucket = eventTime
		}

		m := &spb.ModifyRequest{}
		if err := proto.Unmarshal(p.GetMessage().GetData(), m); err != nil {
			return nil, fmt.Errorf("cannot unmarshal ModifyRequest %s, %v", p, err)
		}
		ts[timeBucket] = append(ts[timeBucket], m)
	}
	return ts, nil
}

type event struct {
	sleep  time.Duration
	events []*spb.ModifyRequest
}

func Schedule(ts timeseries) ([]*event, error) {
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

func awaitTimeout(ctx context.Context, c *fluent.GRIBIClient, t testing.TB, timeout time.Duration) error {
	subctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.Await(subctx, t)
}

func Do(ctx context.Context, t testing.TB, c *fluent.GRIBIClient, sched []*event, timeMultiplier int) {
	c.Start(ctx, t)
	defer c.Stop(t)
	c.StartSending(ctx, t)
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}

	for _, s := range sched {
		extendedTime := s.sleep * time.Duration(timeMultiplier)
		log.Infof("sleeping %s (exaggerated)\n", extendedTime)
		time.Sleep(extendedTime)
		log.Infof("	sending %v\n", s.events)
		c.Modify().Enqueue(t, s.events...)
	}
	if err := awaitTimeout(ctx, c, t, time.Minute); err != nil {
		t.Fatalf("got unexpected error from server - session negotiation, got: %v, want: nil", err)
	}
	t.Logf("Server results, %s", c.Results(t))
}
