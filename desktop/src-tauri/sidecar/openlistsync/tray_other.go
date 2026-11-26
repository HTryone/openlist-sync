//go:build !windows

package main

import "net/http"

var useTray = false

func trayRun(a *App, addr string, mux *http.ServeMux) bool { return false }
