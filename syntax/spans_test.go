package syntax

import (
	"context"
	"reflect"
	"testing"
)

func TestNestedCapturesAndSameNodePatternPriority(t *testing.T) {
	got, err := flatten(context.Background(), []capture{
		{0, 20, 1, String},
		{5, 15, 2, Plain},
		{8, 10, 4, Number},
		{8, 10, 3, Variable},
		{21, 24, 0, Comment},
	}, 25)
	want := []Span{
		{Start: 0, End: 5, Kind: String}, {Start: 5, End: 8, Kind: Plain},
		{Start: 8, End: 10, Kind: Number}, {Start: 10, End: 15, Kind: Plain},
		{Start: 15, End: 20, Kind: String}, {Start: 20, End: 21, Kind: Plain},
		{Start: 21, End: 24, Kind: Comment}, {Start: 24, End: 25, Kind: Plain},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("flatten=%+v %v, want %+v", got, err, want)
	}
}

func TestNoCapturesAndCancelledFlatten(t *testing.T) {
	got, err := flatten(context.Background(), nil, 5)
	if err != nil || !reflect.DeepEqual(got, []Span{{Start: 0, End: 5, Kind: Plain}}) {
		t.Fatalf("plain source: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := flatten(ctx, nil, 5); err != context.Canceled {
		t.Fatalf("cancelled empty capture query: %v", err)
	}
}
