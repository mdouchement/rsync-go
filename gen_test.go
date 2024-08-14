package rsync_test

import (
	"bytes"
	"io"
	"math/rand"
	"testing"

	"github.com/minio/rsync-go"
)

type pair struct {
	Source, Target content
	Description    string
}
type content struct {
	Len   int
	Seed  int64
	Alter int
	Data  []byte
}

func newRandomReader(seed, size int64) io.Reader {
	return io.LimitReader(rand.New(rand.NewSource(seed)), size)
}

func (c *content) Fill() {
	c.Data = make([]byte, c.Len)
	src := rand.NewSource(c.Seed)

	var err error
	c.Data, err = io.ReadAll(newRandomReader(c.Seed, int64(c.Len)))
	if err != nil {
		panic(err)
	}

	if c.Alter > 0 {
		r := rand.New(src)
		for i := 0; i < c.Alter; i++ {
			at := r.Intn(len(c.Data))
			c.Data[at] += byte(r.Int())
		}
	}
}

func Test_GenData(t *testing.T) {
	// Use a seeded generator to get consistent results.
	// This allows testing the package without bundling many test files.

	pairs := []pair{
		{
			Source:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Target:      content{Len: 512*1024 + 89, Seed: 42, Alter: 5},
			Description: "Same length, slightly different content.",
		},
		{
			Source:      content{Len: 512*1024 + 89, Seed: 9824, Alter: 0},
			Target:      content{Len: 512*1024 + 89, Seed: 2345, Alter: 0},
			Description: "Same length, very different content.",
		},
		{
			Source:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Target:      content{Len: 256*1024 + 19, Seed: 42, Alter: 0},
			Description: "Target shorter then source, same content.",
		},
		{
			Source:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Target:      content{Len: 256*1024 + 19, Seed: 42, Alter: 5},
			Description: "Target shorter then source, slightly different content.",
		},
		{
			Source:      content{Len: 256*1024 + 19, Seed: 42, Alter: 0},
			Target:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Description: "Source shorter then target, same content.",
		},
		{
			Source:      content{Len: 512*1024 + 89, Seed: 42, Alter: 5},
			Target:      content{Len: 256*1024 + 19, Seed: 42, Alter: 0},
			Description: "Source shorter then target, slightly different content.",
		},
		{
			Source:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Target:      content{Len: 0, Seed: 42, Alter: 0},
			Description: "Target empty and source has content.",
		},
		{
			Source:      content{Len: 0, Seed: 42, Alter: 0},
			Target:      content{Len: 512*1024 + 89, Seed: 42, Alter: 0},
			Description: "Source empty and target has content.",
		},
		{
			Source:      content{Len: 872, Seed: 9824, Alter: 0},
			Target:      content{Len: 235, Seed: 2345, Alter: 0},
			Description: "Source and target both smaller then a block size.",
		},
	}
	rs := &rsync.RSync{}
	rsDelta := &rsync.RSync{}
	for _, p := range pairs {
		(&p.Source).Fill()
		(&p.Target).Fill()

		sourceBuffer := bytes.NewReader(p.Source.Data)
		targetBuffer := bytes.NewReader(p.Target.Data)

		sig := make([]rsync.BlockHash, 0, 10)
		err := rs.CreateSignature(targetBuffer, func(bl rsync.BlockHash) error {
			sig = append(sig, bl)
			return nil
		})
		if err != nil {
			t.Errorf("Failed to create signature: %s", err)
		}
		opsOut := make(chan rsync.Operation)
		go func() {
			var blockCt, blockRangeCt, dataCt, bytes int
			defer close(opsOut)
			err := rsDelta.CreateDelta(sourceBuffer, sig, func(op rsync.Operation) error {
				switch op.Type {
				case rsync.OpBlockRange:
					blockRangeCt++
				case rsync.OpBlock:
					blockCt++
				case rsync.OpData:
					// Copy data buffer so it may be reused in internal buffer.
					b := make([]byte, len(op.Data))
					copy(b, op.Data)
					op.Data = b
					dataCt++
					bytes += len(op.Data)
				}
				opsOut <- op
				return nil
			})
			t.Logf("Range Ops:%5d, Block Ops:%5d, Data Ops: %5d, Data Len: %5dKiB, For %s.", blockRangeCt, blockCt, dataCt, bytes/1024, p.Description)
			if err != nil {
				t.Errorf("Failed to create delta: %s", err)
			}
		}()

		result := new(bytes.Buffer)

		targetBuffer.Seek(0, 0)
		err = rs.ApplyDelta(result, targetBuffer, opsOut)
		if err != nil {
			t.Errorf("Failed to apply delta: %s", err)
		}

		if result.Len() != len(p.Source.Data) {
			t.Errorf("Result not same size as source: %s", p.Description)
		} else if bytes.Equal(result.Bytes(), p.Source.Data) == false {
			t.Errorf("Result is different from the source: %s", p.Description)
		}

		p.Source.Data = nil
		p.Target.Data = nil
	}
}
