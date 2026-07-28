// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type profiler struct {
	cpuFile *os.File
	stopCh  chan struct{}
}

func (a *app) startProfiler() error {
	dir := filepath.Dir(a.Config.AppDBPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(dir, ".wandersort-cpu.prof"))
	if err != nil {
		return err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return err
	}

	p := &profiler{cpuFile: f, stopCh: make(chan struct{})}
	go p.memLogger(a.Log)
	a.profiler = p
	a.Log.Info("geek: CPU profiling started, memstats every 1s", logger.UserKey, true)
	return nil
}

func (a *app) stopProfiler() {
	if a.profiler == nil {
		return
	}
	pprof.StopCPUProfile()
	a.profiler.cpuFile.Close()
	close(a.profiler.stopCh)

	dir := filepath.Dir(a.Config.AppDBPath)
	heapFile, err := os.Create(filepath.Join(dir, ".wandersort-heap.prof"))
	if err != nil {
		a.Log.Error("geek: failed to create heap profile", "error", err)
	} else {
		runtime.GC()
		pprof.WriteHeapProfile(heapFile)
		heapFile.Close()
	}

	a.profiler = nil
	a.Log.Info("geek: profiling stopped — cpu.prof and heap.prof written", logger.UserKey, true)
}

func (p *profiler) memLogger(log logger.Logger) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Info("geek: memstats",
				"allocMB", m.Alloc/1024/1024,
				"totalAllocMB", m.TotalAlloc/1024/1024,
				"sysMB", m.Sys/1024/1024,
				"heapObjects", m.HeapObjects,
				"numGC", m.NumGC,
				"goroutines", runtime.NumGoroutine(),
			)
		}
	}
}
