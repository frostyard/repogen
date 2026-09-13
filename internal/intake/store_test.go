package intake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestFileStoreCreateIfAbsentIsAtomicUnderConflict(t *testing.T) {
	store := newFixtureFileStore(t)
	const writers = 16

	start := make(chan struct{})
	created := make([]bool, writers)
	errs := make([]error, writers)
	bodies := make([][]byte, writers)
	var wait sync.WaitGroup
	for index := range writers {
		bodies[index] = bytes.Repeat([]byte{byte(index + 1)}, index+1)
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			created[index], errs[index] = store.CreateIfAbsent(
				context.Background(),
				"records/shared",
				bytes.NewReader(bodies[index]),
				int64(len(bodies[index])),
			)
		}(index)
	}
	close(start)
	wait.Wait()

	winner := -1
	for index, err := range errs {
		if err != nil {
			t.Fatalf("writer %d error = %v", index, err)
		}
		if created[index] {
			if winner != -1 {
				t.Fatalf("writers %d and %d both reported creation", winner, index)
			}
			winner = index
		}
	}
	if winner == -1 {
		t.Fatal("no writer reported creation")
	}
	reader, err := store.Open(context.Background(), "records/shared")
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if !bytes.Equal(got, bodies[winner]) {
		t.Fatalf("stored bytes do not belong to atomic winner %d", winner)
	}
}

func TestCreateAndVerifyConcurrentConflictFailsClosed(t *testing.T) {
	store := newFixtureFileStore(t)
	start := make(chan struct{})
	bodies := [][]byte{[]byte("first"), []byte("second")}
	errs := make([]error, len(bodies))
	var wait sync.WaitGroup
	for index := range bodies {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			errs[index] = createAndVerify(
				context.Background(),
				store,
				"records/conflict",
				bodies[index],
			)
		}(index)
	}
	close(start)
	wait.Wait()

	successes := 0
	conflicts := 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent create error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, byte conflicts = %d, want 1 each", successes, conflicts)
	}
}
