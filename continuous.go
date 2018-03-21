package continuous

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	gnet "github.com/facebookgo/grace/gracenet"
	"gitlab.meitu.com/gocommons/logbunny"
)

// Continuous is the interface of a basic server
type Continuous interface {
	Serve(lis net.Listener) error
	Stop() error
	GracefulStop() error
}

// Cont keeps your server which implement the Continuous continuously
type Cont struct {
	net      gnet.Net
	name     string
	pid      int
	child    int
	pidfile  string
	cwd      string
	logger   logbunny.Logger
	servers  []*ContServer
	state    ContState
	wg       sync.WaitGroup
	doneChan chan struct{}
}

// ContState indicates the state of Cont
type ContState int

const (
	Running ContState = iota
	Ready
	Stopped
)

func (cs ContState) String() string {
	switch cs {
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	case Ready:
		return "ready"
	}
	return ""
}

// ListenOn some network and address
type ListenOn struct {
	Network string
	Address string
}

// ContServer combines listener, addresss and a continuous
type ContServer struct {
	lis      net.Listener
	srv      Continuous
	listenOn *ListenOn
}

// Option to new a Cont
type Option func(cont *Cont)

// ProcName custom the procname, use os.Args[0] if not set
func ProcName(name string) Option {
	return func(cont *Cont) {
		cont.name = name
	}
}

// WorkDir custom the work dir, use os.Getwd() if not set
func WorkDir(path string) Option {
	return func(cont *Cont) {
		cont.cwd = path
	}
}

// UseLogger set an external logbunny.Logger
func UseLogger(logger logbunny.Logger) Option {
	return func(cont *Cont) {
		cont.logger = logger
	}
}

// PidFile custom the pid file path
func PidFile(filename string) Option {
	return func(cont *Cont) {
		cont.pidfile = filename
	}
}

// New creates a Cont object which upgrades binary continuously
func New(opts ...Option) *Cont {
	dir, _ := os.Getwd()
	cont := &Cont{name: os.Args[0], cwd: dir, pid: os.Getpid()}

	for _, o := range opts {
		o(cont)
	}

	if cont.pidfile == "" {
		cont.pidfile = cont.cwd + "/" + cont.name + ".pid"
	}

	if cont.logger == nil {
		logger, err := logbunny.New(logbunny.WithDebugLevel(), logbunny.WithPID())
		if err != nil {
			fmt.Println(err)
			os.Exit(-1)
		}
		cont.logger = logger
	}

	return cont
}

// AddServer and a server which implement Continuous interface
func (cont *Cont) AddServer(srv Continuous, listenOn *ListenOn) {
	cont.servers = append(cont.servers, &ContServer{srv: srv, listenOn: listenOn})
}

// Serve run all the servers and wait to handle signals
func (cont *Cont) Serve() error {
	cont.logger.Debug("continuous serving")
	if err := cont.writePid(); err != nil {
		return err
	}

	cont.serve()

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGUSR2, syscall.SIGUSR1, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGCHLD)
	cont.logger.Debug("waiting for signals")

	for {
		sig := <-c
		cont.logger.Info("got signal", logbunny.Stringer("value", sig))
		switch sig {
		case syscall.SIGTERM:
			cont.stop()
			return nil
		case syscall.SIGQUIT:
			cont.gracefulStop()
			return nil
		case syscall.SIGUSR1:
			if cont.state == Running {
				cont.state = Ready
				cont.closeListeners()
			} else if cont.state == Ready {
				cont.wg.Wait() //wait server goroutines to exit
				cont.serve()   //listen and serve again
				cont.state = Running
			}

		case syscall.SIGUSR2:
			if err := cont.upgrade(); err != nil {
				cont.logger.Error("upgrade binary failed", logbunny.Err(err))
			}

		case syscall.SIGHUP:
			if err := cont.upgrade(); err != nil {
				cont.logger.Error("upgrade binary failed", logbunny.Err(err))
				continue
			}
			if err := cont.gracefulStop(); err != nil {
				cont.logger.Error("upgrade binary failed", logbunny.Err(err))
				continue
			}
			return nil

		case syscall.SIGCHLD:
			p, err := os.FindProcess(cont.child)
			if err != nil {
				cont.logger.Error("find process failed", logbunny.Err(err))
			}
			// wait child process to exit to avoid zombie process
			status, err := p.Wait()
			if err != nil {
				cont.logger.Error("wait child process to exit failed", logbunny.Err(err))
			} else {
				cont.logger.Info("child exited", logbunny.Stringer("status", status))
			}

			// rename pidfile to pidfile.old
			if err := os.Rename(cont.pidfile+".old", cont.pidfile); err != nil {
				cont.logger.Error("recover pid file failed", logbunny.Err(err))
			}
		}
	}
}

func (cont *Cont) stop() error {
	close(cont.doneChan)
	for _, server := range cont.servers {
		if err := server.srv.Stop(); err != nil {
			return err
		}
	}
	cont.state = Stopped
	return nil
}

func (cont *Cont) gracefulStop() error {
	close(cont.doneChan)
	for _, server := range cont.servers {
		if err := server.srv.GracefulStop(); err != nil {
			return err
		}
	}
	cont.state = Stopped
	return nil
}

func (cont *Cont) upgrade() error {
	// rename pidfile to pidfile.old
	if err := os.Rename(cont.pidfile, cont.pidfile+".old"); err != nil {
		cont.logger.Warn("rename pid file failed", logbunny.Err(err))
	}

	pid, err := cont.net.StartProcess()
	if err != nil {
		return err
	}
	cont.logger.Info("new process started", logbunny.Int("child", pid))
	cont.child = pid
	return nil
}

func (cont *Cont) closeListeners() {
	// close chan to notify Serve to exit and ignore
	close(cont.doneChan)
	for _, server := range cont.servers {
		if err := server.lis.Close(); err != nil {
			cont.logger.Error("close listener failed", logbunny.Err(err), logbunny.String("listenon", server.listenOn.Address))
		}
	}
	// gracenet internal stores all the active listeners. When we close listeners here, we can not notify gracenet about this
	// so it will keep those closed listeners and try to pass to child process which cause errors, so we reinit net here
	cont.net = gnet.Net{}
}

func (cont *Cont) serve() error {
	cont.doneChan = make(chan struct{})

	for _, server := range cont.servers {
		lis, err := cont.net.Listen(server.listenOn.Network, server.listenOn.Address)
		if err != nil {
			return err
		}
		server.lis = lis

		cont.wg.Add(1)
		go func(server *ContServer) {
			done := false
			if err := server.srv.Serve(server.lis); err != nil {
				select {
				case <-cont.doneChan:
					done = true // ignore error which caused by Stop/GracefulStop
				default:
				}
				if !done {
					cont.logger.Error("serve failed", logbunny.Err(err), logbunny.String("listen", server.listenOn.Address))
				}
			}
			cont.wg.Done()
		}(server)
	}

	cont.state = Running
	return nil
}

func (cont *Cont) writePid() error {
	return ioutil.WriteFile(cont.pidfile, []byte(fmt.Sprint(cont.pid)), 0644)
}

// Status return the current status
func (cont *Cont) Status() ContState {
	return cont.state
}
