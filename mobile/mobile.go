// Package mobile is the small Android-facing interface to the shared connector.
package mobile

import (
	"context"
	"errors"
	"fmt"
	"local/bedrock-lan-connector/connector"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var mu sync.Mutex
var current *session
var lines string
var logPath string

// SetLogPath enables bounded diagnostics in the Android app's private files.
func SetLogPath(path string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	logPath = path
	return nil
}

type session struct {
	cancel      context.CancelFunc
	done, ready chan struct{}
	once        sync.Once
	err         error
	readyCount  int
}
type logWriter struct{ s *session }

func (w logWriter) Write(p []byte) (int, error) {
	mu.Lock()
	lines += string(p)
	if logPath != "" {
		if info, err := os.Stat(logPath); err == nil && info.Size() > 128*1024 {
			_ = os.Rename(logPath, logPath+".previous")
		}
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			_, _ = f.Write(p)
			_ = f.Close()
		}
	}
	if len(lines) > 24000 {
		lines = lines[len(lines)-24000:]
	}
	if strings.Contains(string(p), "LAN connector ready") || strings.Contains(string(p), "Java LAN broadcaster ready") {
		w.s.readyCount--
		if w.s.readyCount == 0 {
			w.s.once.Do(func() { close(w.s.ready) })
		}
	}
	mu.Unlock()
	return len(p), nil
}

// Start waits until the LAN listener is ready, or returns a useful startup error.
func Start(target, name, iface string) error {
	return startSelected(target, "", name, iface, true, false)
}

func StartSelected(bedrockTarget, javaTarget, iface string, bedrock, java bool) error {
	return startSelected(bedrockTarget, javaTarget, "Minecraft LAN Broadcaster - Phone", iface, bedrock, java)
}
func startSelected(target, javaTarget, name, iface string, bedrock, java bool) error {
	if !bedrock && !java {
		return errors.New("select Bedrock or Java")
	}
	if bedrock {
		if _, _, err := connector.ParseTarget(target); err != nil {
			return err
		}
	}
	if java {
		if _, _, err := connector.ParseTarget(javaTarget); err != nil {
			return err
		}
	}
	mu.Lock()
	if current != nil {
		mu.Unlock()
		return errors.New("connector is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{cancel: cancel, done: make(chan struct{}), ready: make(chan struct{})}
	if bedrock {
		s.readyCount++
	}
	if java {
		s.readyCount++
	}
	current = s
	lines = ""
	mu.Unlock()
	logger := slog.New(slog.NewTextHandler(logWriter{s}, nil))
	go func() {
		results := make(chan error, 2)
		count := 0
		if bedrock {
			count++
			go func() {
				results <- connector.RunStaged(ctx, connector.Config{Target: target, Name: name, Interface: iface, DiscoveryPort: 7551, MediaPort: 7552}, logger)
			}()
		}
		if java {
			count++
			go func() {
				results <- connector.RunJava(ctx, connector.JavaConfig{Target: javaTarget, Interface: iface, Port: 25565}, logger)
			}()
		}
		var err error
		for i := 0; i < count; i++ {
			if e := <-results; e != nil && err == nil {
				err = e
			}
			cancel()
		}
		mu.Lock()
		s.err = err
		if err != nil {
			lines += "Stopped: " + err.Error() + "\n"
		}
		if current == s {
			current = nil
		}
		mu.Unlock()
		close(s.done)
	}()
	select {
	case <-s.ready:
		return nil
	case <-s.done:
		return fmt.Errorf("could not start: %v", s.err)
	case <-time.After(15 * time.Second):
		cancel()
		return errors.New("startup timed out")
	}
}

func Stop() error {
	mu.Lock()
	s := current
	mu.Unlock()
	if s == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("listener shutdown timed out")
	}
}
func IsRunning() bool                 { mu.Lock(); defer mu.Unlock(); return current != nil }
func Logs() string                    { mu.Lock(); defer mu.Unlock(); return lines }
func CheckTarget(target string) error { return connector.CheckTarget(target) }
