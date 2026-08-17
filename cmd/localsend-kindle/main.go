package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"localsend-kual/internal/app"
)

const version = "0.1.8"

func common(fs *flag.FlagSet) *string {
	return fs.String("root", "/mnt/us/extensions/localsend", "extension root")
}

func load(root string) (app.Config, *app.Identity, *app.StateStore, error) {
	cfg, err := app.LoadConfig(root)
	if err != nil {
		return cfg, nil, nil, err
	}
	id, err := app.LoadOrCreateIdentity(root)
	if err != nil {
		return cfg, nil, nil, err
	}
	return cfg, id, app.NewStateStore(root), nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: localsend-kindle <serve|send|status|menu|peers|selftest|firewall-cleanup|version>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "serve":
		serve(os.Args[2:])
	case "send":
		send(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "menu":
		menu(os.Args[2:])
	case "peers":
		peers(os.Args[2:])
	case "selftest":
		selftest(os.Args[2:])
	case "firewall-cleanup":
		firewallCleanup(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	root := common(fs)
	duration := fs.Int("duration", 0, "minutes; 0 means unlimited")
	compatHTTP := fs.Bool("compat-http", false, "force LocalSend v2.2 HTTP compatibility mode")
	receiveDir := fs.String("receive-dir", "", "override receive directory")
	fs.Parse(args)
	cfg, id, state, err := load(*root)
	if err != nil {
		log.Fatal(err)
	}
	if *compatHTTP {
		cfg.Encryption = false
	}
	if *receiveDir != "" {
		cfg.ReceiveDir = *receiveDir
		if err := os.MkdirAll(cfg.ReceiveDir, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(*root, "logs"), 0o755)
	logPath := filepath.Join(*root, "logs", "localsend.log")
	lf, err := app.OpenRotatingLog(logPath, app.DefaultLogMaxBytes)
	if err != nil {
		log.Fatalf("open log: %v", err)
	}
	defer lf.Close()
	logger := log.New(lf, "", log.LstdFlags)
	releaseLock, err := app.AcquireDaemonLock(*root)
	if err != nil {
		logger.Printf("startup blocked: %v", err)
		log.Fatal(err)
	}
	defer releaseLock()
	if removed, err := app.CleanupStalePartials(cfg.ReceiveDir, logger); err != nil {
		logger.Printf("partial cleanup scan failed: %v", err)
	} else if removed > 0 {
		logger.Printf("partial cleanup removed %d stale file(s)", removed)
	}
	logger.Printf("LocalSend-KUAL stability wrapper v0.1.8 active; frozen dual-platform transfer core=v0.1.7")
	s := app.NewServer(*root, cfg, id, state, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *duration > 0 {
		ctx2, c := context.WithTimeout(ctx, time.Duration(*duration)*time.Minute)
		defer c()
		ctx = ctx2
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	go func() {
		for x := range sig {
			if x == syscall.SIGUSR1 {
				s.TriggerAnnouncement()
				continue
			}
			cancel()
			return
		}
	}()
	if err := s.Run(ctx); err != nil {
		logger.Printf("fatal: %v", err)
		log.Fatal(err)
	}
}

func send(args []string) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	root := common(fs)
	peerQ := fs.String("peer", "", "peer fingerprint/alias/IP")
	outbox := fs.String("outbox", "", "directory to send")
	pin := fs.String("pin", "", "remote PIN")
	fs.Parse(args)
	cfg, id, state, err := load(*root)
	if err != nil {
		log.Fatal(err)
	}
	peer, err := state.FindPeer(*peerQ)
	if err != nil {
		log.Fatal(err)
	}
	dir := *outbox
	if dir == "" {
		dir = cfg.OutboxDir
	}
	if *pin == "" {
		*pin = cfg.SendPIN
	}
	paths, err := app.ListOutbox(dir)
	if err != nil {
		log.Fatal(err)
	}
	s := app.NewServer(*root, cfg, id, state, log.New(os.Stderr, "", log.LstdFlags))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := s.SendFiles(ctx, peer, paths, *pin); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("sent %d file(s) to %s\n", len(paths), peer.Alias)
}

func status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := common(fs)
	short := fs.Bool("short", false, "short text")
	fs.Parse(args)
	if *short {
		fmt.Println(app.ShortStatus(*root))
		return
	}
	fmt.Println(app.ShortStatus(*root))
}

func menu(args []string) {
	fs := flag.NewFlagSet("menu", flag.ExitOnError)
	root := common(fs)
	write := fs.String("write", "", "menu path")
	fs.Parse(args)
	state := app.NewStateStore(*root)
	path := *write
	if path == "" {
		path = filepath.Join(*root, "menu.json")
	}
	if err := app.WriteKUALMenu(path, state.Peers()); err != nil {
		log.Fatal(err)
	}
}

func peers(args []string) {
	fs := flag.NewFlagSet("peers", flag.ExitOnError)
	root := common(fs)
	fs.Parse(args)
	for i, p := range app.NewStateStore(*root).Peers() {
		fmt.Printf("%d\t%s\t%s\t%s\t%s:%s\n", i+1, p.Alias, p.Fingerprint, p.Protocol, p.IP, strconv.Itoa(p.Port))
	}
}

func selftest(args []string) {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	root := common(fs)
	fs.Parse(args)
	cfg, err := app.LoadConfig(*root)
	if err != nil {
		fmt.Printf("config=FAIL %v\n", err)
		os.Exit(1)
	}
	result := app.SelfTest(*root, cfg)
	fmt.Print(result.Text())
	if !result.OK {
		os.Exit(1)
	}
}

func firewallCleanup(args []string) {
	fs := flag.NewFlagSet("firewall-cleanup", flag.ExitOnError)
	root := common(fs)
	fs.Parse(args)
	cfg, err := app.LoadConfig(*root)
	if err != nil {
		log.Fatal(err)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	if err := app.CleanupFirewall(cfg.Interface, cfg.Port, logger); err != nil {
		log.Fatal(err)
	}
}
