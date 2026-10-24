package appagent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreThreadsMessagesPagingAndUnread(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	th, err := st.CreateThread(ctx, "t1", "tachyon", "Tachyon", now)
	if err != nil || th.ID != "t1" || th.Owner != "tachyon" || th.Runtime != "nanite" {
		t.Fatalf("thread: %+v %v", th, err)
	}
	if again, err := st.CreateThread(ctx, "t1", "tachyon", "Renamed?", now); err != nil || again.Title != "Tachyon" {
		t.Fatalf("create is idempotent: %+v %v", again, err)
	}
	if _, err := st.Thread(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing thread: %v", err)
	}
	for i := 0; i < 3; i++ {
		op, ag, err := st.AppendTurn(ctx, "t1", "q", now.Add(time.Duration(i)*time.Second))
		if err != nil || op.Seq != int64(2*i+1) || ag.Seq != int64(2*i+2) || ag.State != StateStreaming {
			t.Fatalf("turn %d: %+v %+v %v", i, op, ag, err)
		}
		if _, _, err := st.AppendTurn(ctx, "t1", "again", now); !errors.Is(err, ErrBusy) {
			t.Fatalf("append while streaming: %v", err)
		}
		if err := st.SaveAgent(ctx, ag.ID, "answer", []Tool{{ID: "c", Name: "n", Status: "ok"}}, StateComplete, "", "turn", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.AppendTurn(ctx, "nope", "x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("append to missing thread: %v", err)
	}
	page, more, err := st.Messages(ctx, "t1", 0, 4)
	if err != nil || !more || len(page) != 4 || page[0].Seq != 3 || page[3].Seq != 6 {
		t.Fatalf("newest page: %+v more=%v %v", page, more, err)
	}
	older, more, _ := st.Messages(ctx, "t1", page[0].Seq, 4)
	if more || len(older) != 2 || older[0].Seq != 1 || older[1].Seq != 2 {
		t.Fatalf("older page: %+v more=%v", older, more)
	}
	if page[1].Role != RoleAgent || page[1].Body != "answer" || len(page[1].Tools) != 1 || page[1].RuntimeTurn != "turn" {
		t.Fatalf("stored agent message: %+v", page[1])
	}
	th, _ = st.Thread(ctx, "t1")
	if th.Unread != 3 || th.LastPreview != "answer" {
		t.Fatalf("unread=%d preview=%q", th.Unread, th.LastPreview)
	}
	if err := st.MarkRead(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if th, _ = st.Thread(ctx, "t1"); th.Unread != 0 {
		t.Fatalf("unread after read: %d", th.Unread)
	}
	if err := st.MarkRead(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mark missing: %v", err)
	}
	if streaming, _ := st.Streaming(ctx); len(streaming) != 0 {
		t.Fatalf("streaming: %+v", streaming)
	}
}
