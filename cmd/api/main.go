package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"beprod/internal/admin"
	"beprod/internal/app"
	"beprod/internal/auth"
	"beprod/internal/config"
	"beprod/internal/db"
	"beprod/internal/mail"
	"beprod/internal/recurring"
	"beprod/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	gdb, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		log.Fatal(err)
	}
	if err := admin.Bootstrap(gdb, cfg); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go recurring.RunScheduler(ctx, gdb)
	f := server.New(app.Deps{DB: gdb, Config: cfg, Mailer: mail.LogMailer{}, VerifyGoogle: auth.NewGoogleVerifier(cfg.GoogleClientID)})
	go func() { <-ctx.Done(); f.Shutdown() }()
	log.Fatal(f.Listen(":" + cfg.Port))
}
