package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

var (
	errAbortStreamBeforeWrite = errors.New("abort stream before first client write")
	errStopStreamAfterWrite   = errors.New("stop stream after current client write")
)

type stopStreamAfterWriteError struct {
	writeBytes int
}

func (e *stopStreamAfterWriteError) Error() string {
	return errStopStreamAfterWrite.Error()
}

func (e *stopStreamAfterWriteError) Unwrap() error {
	return errStopStreamAfterWrite
}

// ============================================================================
// 流式传输数据结构
// ============================================================================

// streamReadStats 流式传输统计信息
type streamReadStats struct {
	readCount    int
	totalBytes   int64
	firstByteSec float64 // 首字节读取耗时（秒），attachFirstByteDetector 写入
}

// firstByteDetector 检测首字节读取时间和传输统计的Reader包装器
type firstByteDetector struct {
	io.ReadCloser
	stats       *streamReadStats
	onFirstRead func()
	onBytesRead func(int64) // 可选：每次读取后的回调（nil 时不触发）
}

// Read 实现io.Reader接口，记录读取统计
func (r *firstByteDetector) Read(p []byte) (n int, err error) {
	n, err = r.ReadCloser.Read(p)
	if n > 0 {
		// 记录统计信息
		if r.stats != nil {
			r.stats.readCount++
			r.stats.totalBytes += int64(n)
		}
		// 触发首次读取回调
		if r.onFirstRead != nil {
			r.onFirstRead()
			r.onFirstRead = nil // 只触发一次
		}
		// 触发字节读取回调（可选）
		if r.onBytesRead != nil {
			r.onBytesRead(int64(n))
		}
	}
	return
}

// ============================================================================
// 流式传输核心函数
// ============================================================================

func streamCopyWithBufferSize(ctx context.Context, src io.Reader, dst http.ResponseWriter, onData func([]byte) error, bufSize int) error {
	stopCloseOnCancel := closeReaderOnContextCancel(ctx, src)
	defer stopCloseOnCancel()

	buf := make([]byte, bufSize)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := src.Read(buf)
		if n > 0 {
			stopAfterWrite := false
			writeBytes := n
			// [FIX] 2026-01: 先 Feed 数据到 parser，再写入客户端
			// 原因：即使写入失败（客户端断开），也需要检测流结束标志（如 response.completed）
			// 这样当上游完整返回但客户端取消时，可以正确识别为"流完整"而非 499
			if onData != nil {
				if hookErr := onData(buf[:n]); hookErr != nil {
					if errors.Is(hookErr, errAbortStreamBeforeWrite) {
						return hookErr
					}
					if errors.Is(hookErr, errStopStreamAfterWrite) {
						stopAfterWrite = true
						var stopErr *stopStreamAfterWriteError
						if errors.As(hookErr, &stopErr) && stopErr.writeBytes >= 0 && stopErr.writeBytes <= n {
							writeBytes = stopErr.writeBytes
						}
					}
					_ = hookErr // 钩子错误不中断流传输（容错设计）
				}
			}
			if _, writeErr := dst.Write(buf[:writeBytes]); writeErr != nil {
				return writeErr
			}
			if flusher, ok := dst.(http.Flusher); ok {
				flusher.Flush()
			}
			if stopAfterWrite {
				return nil
			}
		}
		if err != nil {
			return normalizeStreamReadError(ctx, err)
		}
	}
}

// normalizeStreamReadError 保留 Read 的真实终止原因。
// context 取消会主动 Close 源，底层可能因此返回 EOF；此时取消/超时必须优先。
func normalizeStreamReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err == io.EOF {
		return nil
	}
	return err
}

func closeReaderOnContextCancel(ctx context.Context, src io.Reader) func() {
	closer, ok := src.(io.Closer)
	if !ok {
		return func() {}
	}
	stop := context.AfterFunc(ctx, func() {
		_ = closer.Close()
	})
	return func() {
		_ = stop()
	}
}

// deferredResponseWriter 延迟提交响应头，允许在首个可见输出前中止本次流并切换到其他上游。
type deferredResponseWriter struct {
	target    http.ResponseWriter
	header    http.Header
	status    int
	committed bool
	buffer    bytes.Buffer
}

func newDeferredResponseWriter(target http.ResponseWriter) *deferredResponseWriter {
	return &deferredResponseWriter{
		target: target,
		header: make(http.Header),
	}
}

func (w *deferredResponseWriter) Header() http.Header {
	return w.header
}

func (w *deferredResponseWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.status = status
}

func (w *deferredResponseWriter) Write(p []byte) (int, error) {
	if !w.committed {
		return w.buffer.Write(p)
	}
	return w.target.Write(p)
}

func (w *deferredResponseWriter) Flush() {
	if !w.committed {
		return
	}
	if flusher, ok := w.target.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *deferredResponseWriter) Commit() error {
	if w.committed {
		return nil
	}
	for key, values := range w.header {
		dstValues := append([]string(nil), values...)
		w.target.Header()[key] = dstValues
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.target.WriteHeader(status)
	w.committed = true
	if w.buffer.Len() > 0 {
		if _, err := w.target.Write(w.buffer.Bytes()); err != nil {
			return err
		}
		w.buffer.Reset()
	}
	return nil
}

func (w *deferredResponseWriter) Committed() bool {
	return w.committed
}

// streamCopy 流式复制（支持flusher与ctx取消）
// 从proxy.go提取，遵循SRP原则
// 简化实现：直接循环读取与写入，避免为每次读取创建goroutine导致泄漏
// 首字节超时由 requestContext 统一管控（firstByteTimeout + context.AfterFunc 关闭 body），此处不再重复实现
func streamCopy(ctx context.Context, src io.Reader, dst http.ResponseWriter, onData func([]byte) error) error {
	return streamCopyWithBufferSize(ctx, src, dst, onData, StreamBufferSize)
}

// streamCopySSE SSE专用流式复制（使用小缓冲区优化延迟）
// [INFO] SSE优化（2025-10-17）：4KB缓冲区降低首Token延迟60~80%
// [INFO] 支持数据钩子（2025-11）：允许SSE usage解析器增量处理数据流
// 设计原则：SSE事件通常200B-2KB，小缓冲区避免事件积压
func streamCopySSE(ctx context.Context, src io.Reader, dst http.ResponseWriter, onData func([]byte) error) error {
	tracker := &responsesSSETerminalTracker{}
	err := streamCopyWithBufferSize(ctx, src, dst, func(data []byte) error {
		tracker.observe(data)
		if onData == nil {
			return nil
		}
		return onData(data)
	}, SSEBufferSize)
	if err == nil && tracker.sawResponseEvent && !tracker.sawTerminalEvent {
		return io.ErrUnexpectedEOF
	}
	return err
}

type responsesSSETerminalTracker struct {
	eventBuffer      bytes.Buffer
	eventType        string
	dataLineCount    int
	lineOverflow     bool
	lineLength       int
	lineDataStart    int
	lineDataFed      int
	lineField        responsesSSELineField
	jsonType         responsesJSONTypeScanner
	sawResponseEvent bool
	sawTerminalEvent bool
}

const maxResponsesSSELineBytes = 64 << 10

type responsesSSELineField uint8

const (
	responsesSSELineUnknown responsesSSELineField = iota
	responsesSSELineData
	responsesSSELineOther
)

type responsesJSONTypeScanner struct {
	depth          int
	expectingKey   bool
	pendingKey     string
	topLevelValue  bool
	inString       bool
	escape         bool
	stringRole     responsesJSONStringRole
	stringBuffer   []byte
	stringOverflow bool
	eventType      string
}

type responsesJSONStringRole uint8

const (
	responsesJSONStringOther responsesJSONStringRole = iota
	responsesJSONStringKey
	responsesJSONStringTypeValue
)

const maxResponsesJSONTokenBytes = 64

func (t *responsesSSETerminalTracker) observe(data []byte) {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			t.appendLine(data)
			return
		}
		t.appendLine(data[:newline])
		t.finishLine()
		data = data[newline+1:]
	}
}

func (t *responsesSSETerminalTracker) appendLine(data []byte) {
	oldLength := t.lineLength
	t.retainLine(data)
	t.lineLength += len(data)
	t.classifyLine()
	if t.lineField == responsesSSELineData {
		t.consumeLineData(data, oldLength)
	}
}

func (t *responsesSSETerminalTracker) retainLine(data []byte) {
	if t.lineOverflow {
		return
	}
	remaining := maxResponsesSSELineBytes - t.eventBuffer.Len()
	if len(data) > remaining {
		_, _ = t.eventBuffer.Write(data[:remaining])
		t.lineOverflow = true
		return
	}
	_, _ = t.eventBuffer.Write(data)
}

func (t *responsesSSETerminalTracker) classifyLine() {
	if t.lineField != responsesSSELineUnknown {
		return
	}
	linePrefix := t.eventBuffer.Bytes()
	dataField := []byte("data:")
	if len(linePrefix) < len(dataField) {
		if bytes.HasPrefix(dataField, linePrefix) {
			return
		}
		t.lineField = responsesSSELineOther
		return
	}
	if bytes.HasPrefix(linePrefix, dataField) {
		t.lineField = responsesSSELineData
		t.lineDataStart = len(dataField)
		if t.dataLineCount > 0 {
			t.jsonType.observe([]byte{'\n'})
		}
		t.dataLineCount++
		return
	}
	t.lineField = responsesSSELineOther
}

func (t *responsesSSETerminalTracker) consumeLineData(data []byte, oldLength int) {
	retainedEnd := min(t.lineLength, t.eventBuffer.Len())
	if t.lineDataFed < retainedEnd {
		start := max(t.lineDataStart, t.lineDataFed)
		if start < retainedEnd {
			t.jsonType.observe(t.eventBuffer.Bytes()[start:retainedEnd])
		}
		t.lineDataFed = retainedEnd
	}
	if t.lineDataFed >= t.lineLength {
		return
	}

	inputStart := max(t.lineDataFed-oldLength, 0)
	if inputStart < len(data) {
		t.jsonType.observe(data[inputStart:])
	}
	t.lineDataFed = t.lineLength
}

func (t *responsesSSETerminalTracker) finishLine() {
	line := t.eventBuffer.Bytes()
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		t.finishEvent()
	} else {
		t.observeLine(line)
	}
	t.eventBuffer.Reset()
	t.lineOverflow = false
	t.lineLength = 0
	t.lineDataStart = 0
	t.lineDataFed = 0
	t.lineField = responsesSSELineUnknown
}

func (t *responsesSSETerminalTracker) observeLine(line []byte) {
	if value, ok := bytes.CutPrefix(line, []byte("event:")); ok {
		t.eventType = string(bytes.TrimSpace(value))
	}
}

func (t *responsesSSETerminalTracker) finishEvent() {
	dataType := strings.TrimSpace(t.jsonType.eventType)
	eventType := strings.TrimSpace(t.eventType)
	if strings.HasPrefix(eventType, "response.") || strings.HasPrefix(dataType, "response.") {
		t.sawResponseEvent = true
	}
	if isResponsesTerminalEvent(eventType) || isResponsesTerminalEvent(dataType) {
		t.sawTerminalEvent = true
	}
	t.eventType = ""
	t.dataLineCount = 0
	t.jsonType.reset()
}

func isResponsesTerminalEvent(eventType string) bool {
	switch eventType {
	case "response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

func (s *responsesJSONTypeScanner) observe(data []byte) {
	for _, b := range data {
		s.consume(b)
	}
}

func (s *responsesJSONTypeScanner) consume(b byte) {
	if s.inString {
		s.consumeStringByte(b)
		return
	}

	switch b {
	case '"':
		s.startString()
	case '{', '[':
		if s.depth == 0 {
			s.depth = 1
			s.expectingKey = b == '{'
			s.pendingKey = ""
			s.topLevelValue = false
			return
		}
		s.depth++
	case '}', ']':
		switch {
		case s.depth > 1:
			s.depth--
			if s.depth == 1 {
				s.expectingKey = false
				s.topLevelValue = false
			}
		case s.depth == 1:
			s.depth = 0
			s.expectingKey = false
			s.pendingKey = ""
			s.topLevelValue = false
		}
	case ':':
		if s.depth == 1 && !s.expectingKey {
			s.topLevelValue = true
		}
	case ',':
		if s.depth == 1 {
			s.expectingKey = true
			s.pendingKey = ""
			s.topLevelValue = false
		}
	}
}

func (s *responsesJSONTypeScanner) startString() {
	s.inString = true
	s.escape = false
	s.stringBuffer = s.stringBuffer[:0]
	s.stringOverflow = false
	switch {
	case s.depth == 1 && s.expectingKey:
		s.stringRole = responsesJSONStringKey
	case s.depth == 1 && s.pendingKey == "type" && s.topLevelValue:
		s.stringRole = responsesJSONStringTypeValue
	default:
		s.stringRole = responsesJSONStringOther
	}
}

func (s *responsesJSONTypeScanner) consumeStringByte(b byte) {
	if s.escape {
		s.escape = false
		s.appendStringByte(b)
		return
	}
	switch b {
	case '\\':
		s.appendStringByte(b)
		s.escape = true
	case '"':
		s.finishString()
	default:
		s.appendStringByte(b)
	}
}

func (s *responsesJSONTypeScanner) appendStringByte(b byte) {
	if s.stringOverflow {
		return
	}
	if len(s.stringBuffer) >= maxResponsesJSONTokenBytes {
		s.stringOverflow = true
		s.stringBuffer = s.stringBuffer[:0]
		return
	}
	s.stringBuffer = append(s.stringBuffer, b)
}

func (s *responsesJSONTypeScanner) finishString() {
	s.inString = false
	switch s.stringRole {
	case responsesJSONStringKey:
		if s.stringOverflow {
			s.pendingKey = ""
		} else {
			s.pendingKey = s.stringToken()
		}
		s.expectingKey = false
	case responsesJSONStringTypeValue:
		if !s.stringOverflow {
			s.eventType = s.stringToken()
		}
		s.topLevelValue = false
	case responsesJSONStringOther:
		if s.depth == 1 {
			s.topLevelValue = false
		}
	}
	s.stringRole = responsesJSONStringOther
}

func (s *responsesJSONTypeScanner) stringToken() string {
	value, err := strconv.Unquote(`"` + string(s.stringBuffer) + `"`)
	if err != nil {
		return string(s.stringBuffer)
	}
	return value
}

func (s *responsesJSONTypeScanner) reset() {
	s.depth = 0
	s.expectingKey = false
	s.pendingKey = ""
	s.topLevelValue = false
	s.inString = false
	s.escape = false
	s.stringRole = responsesJSONStringOther
	s.stringBuffer = s.stringBuffer[:0]
	s.stringOverflow = false
	s.eventType = ""
}

func streamTransformSSEEvents(
	ctx context.Context,
	src io.Reader,
	dst http.ResponseWriter,
	onRawEvent func([]byte) error,
	transform func([]byte) ([][]byte, error),
) error {
	return streamTransformSSEEventsUntil(ctx, src, dst, onRawEvent, transform, nil)
}

func streamTransformSSEEventsUntil(
	ctx context.Context,
	src io.Reader,
	dst http.ResponseWriter,
	onRawEvent func([]byte) error,
	transform func([]byte) ([][]byte, error),
	stopAfterEvent func() bool,
) error {
	stopCloseOnCancel := closeReaderOnContextCancel(ctx, src)
	defer stopCloseOnCancel()

	reader := bufio.NewReader(src)
	var eventBuf bytes.Buffer

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			eventBuf.Write(line)
			if bytes.Equal(bytes.TrimRight(line, "\r\n"), []byte{}) {
				rawEvent := append([]byte(nil), eventBuf.Bytes()...)
				if len(rawEvent) > 0 {
					if onRawEvent != nil {
						if hookErr := onRawEvent(rawEvent); hookErr != nil {
							if errors.Is(hookErr, errAbortStreamBeforeWrite) {
								return hookErr
							}
							_ = hookErr
						}
					}
					if transform != nil {
						chunks, transformErr := transform(rawEvent)
						if transformErr != nil {
							return transformErr
						}
						for _, chunk := range chunks {
							if len(chunk) == 0 {
								continue
							}
							if _, writeErr := dst.Write(chunk); writeErr != nil {
								return writeErr
							}
							if flusher, ok := dst.(http.Flusher); ok {
								flusher.Flush()
							}
						}
					}
					if stopAfterEvent != nil && stopAfterEvent() {
						return nil
					}
				}
				eventBuf.Reset()
			}
		}

		if err != nil {
			return normalizeStreamReadError(ctx, err)
		}
	}
}
