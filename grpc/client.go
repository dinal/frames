/*
Copyright 2018 Iguazio Systems Ltd.

Licensed under the Apache License, Version 2.0 (the "License") with
an addition restriction as set forth herein. You may not use this
file except in compliance with the License. You may obtain a copy of
the License at http://www.apache.org/licenses/LICENSE-2.0.

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing
permissions and limitations under the License.

In addition, you may not use the software for any purposes that are
illegal under applicable law, and the grant of the foregoing license
under the Apache 2.0 license is conditioned upon your compliance with
such restriction.
*/

package grpc

import (
	"context"
	"fmt"
	"github.com/v3io/frames"
	"github.com/v3io/frames/pb"
	"io"
	"time"

	"github.com/nuclio/logger"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
)

// Client is frames gRPC client
type Client struct {
	client  pb.FramesClient
	session *frames.Session
}

var (
	// Make sure we're implementing frames.Client
	_ frames.Client = &Client{}
)

// NewClient returns a new gRPC client
func NewClient(address string, session *frames.Session, logger logger.Logger) (*Client, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, errors.Wrap(err, "can't create gRPC connection")
	}

	client := &Client{
		client:  pb.NewFramesClient(conn),
		session: session,
	}

	return client, nil
}

func (c *Client) Read(request *frames.ReadRequest) (frames.FrameIterator, error) {
	if request.Session == nil {
		request.Session = c.session
	}

	stream, err := c.client.Read(context.Background(), request)
	if err != nil {
		return nil, err
	}

	it := &frameIterator{
		stream: stream,
	}
	return it, nil
}

func (c *Client) Write(request *frames.WriteRequest) (frames.FrameAppender, error) {
	if request.Session == nil {
		request.Session = c.session
	}

	var frame *pb.Frame
	if request.ImmidiateData != nil {
		var err error
		frame, err = frameToPB(request.ImmidiateData)
		if err != nil {
			return nil, err
		}
	}

	stream, err := c.client.Write(context.Background())
	if err != nil {
		return nil, err
	}

	ireq := &pb.InitialWriteRequest{
		Backend:     request.Backend,
		Table:       request.Table,
		InitialData: frame,
		Expression:  request.Expression,
		More:        request.HaveMore,
	}

	req := &pb.WriteRequest{
		Type: &pb.WriteRequest_Request{
			Request: ireq,
		},
	}

	if err := stream.Send(req); err != nil {
		stream.CloseAndRecv()
		return nil, err
	}

	fa := &frameAppender{
		stream: stream,
		closed: false,
	}

	return fa, nil
}

// Create creates a table
func (c *Client) Create(request *frames.CreateRequest) error {
	if request.Session == nil {
		request.Session = c.session
	}

	_, err := c.client.Create(context.Background(), request)
	return err
}

// Delete deletes data or table
func (c *Client) Delete(request *frames.DeleteRequest) error {
	if request.Session == nil {
		request.Session = c.session
	}

	_, err := c.client.Delete(context.Background(), request)
	return err
}

// Exec executes a command on the backend
func (c *Client) Exec(request *frames.ExecRequest) error {
	if request.Session == nil {
		request.Session = c.session
	}

	_, err := c.client.Exec(context.Background(), request)
	return err
}

type frameIterator struct {
	stream pb.Frames_ReadClient
	frame  frames.Frame
	err    error
	done   bool
}

func (it *frameIterator) Next() bool {
	if it.done || it.err != nil {
		return false
	}

	it.frame = nil
	msg, err := it.stream.Recv()
	if err != nil {
		if err != io.EOF {
			it.err = err
		}
		return false
	}

	frame, err := asFrame(msg)
	if err != nil {
		it.err = err
		return false
	}
	it.frame = frame

	return true
}

func (it *frameIterator) Err() error {
	return it.err
}

func (it *frameIterator) At() frames.Frame {
	return it.frame
}

type frameAppender struct {
	stream pb.Frames_WriteClient
	closed bool
}

func (fa *frameAppender) Add(frame frames.Frame) error {
	if fa.closed {
		return fmt.Errorf("stream closed")
	}

	fMsg, err := frameToPB(frame)
	if err != nil {
		fa.stream.CloseAndRecv()
		fa.closed = true
		return err
	}

	msg := &pb.WriteRequest{
		Type: &pb.WriteRequest_Frame{
			Frame: fMsg,
		},
	}

	if err := fa.stream.Send(msg); err != nil {
		fa.stream.CloseAndRecv()
		fa.closed = true
		return err
	}

	return nil
}

func (fa *frameAppender) WaitForComplete(timeout time.Duration) error {
	if fa.closed {
		return fmt.Errorf("stream closed")
	}

	// TODO: timeout
	_, err := fa.stream.CloseAndRecv()
	return err
}