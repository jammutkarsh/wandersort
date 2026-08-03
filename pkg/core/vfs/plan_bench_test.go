// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// benchMasters builds a library-shaped set of masters: n files spread over a
// year and over a handful of places, with the jitter a real camera leaves.
// Plan reads only these fields, so nothing here needs a database.
func benchMasters(n int) []masterFile {
	places := [][2]float64{
		{15.5439, 73.7553},
		{22.7196, 75.8577},
		{18.5204, 73.8567},
		{19.0760, 72.8777},
		{28.6139, 77.2090},
		{48.8566, 2.3522},
	}
	masters := make([]masterFile, n)
	for i := range masters {
		p := places[i%len(places)]
		lat, lon := p[0]+float64(i%37)*0.001, p[1]+float64(i%29)*0.001
		masters[i] = newBenchMaster(i, lat, lon)
	}
	return masters
}

// scatteredMasters is the cache's worst case: every file far enough from the
// last to land in its own lookup square, i.e. a library of one-off places
// rather than a few trips. This is the shape the worker pool exists for.
func scatteredMasters(n int) []masterFile {
	masters := make([]masterFile, n)
	for i := range masters {
		masters[i] = newBenchMaster(i, 15.0+float64(i%200)*0.05, 73.0+float64(i/200)*0.05)
	}
	return masters
}

func newBenchMaster(i int, lat, lon float64) masterFile {
	dto := fmt.Sprintf("2024:%02d:%02d %02d:%02d:00", i%12+1, i%28+1, i%24, i%60)
	dir := fmt.Sprintf("/src/trip%d", i%20)
	name := fmt.Sprintf("IMG_%05d.HEIC", i)
	return masterFile{
		FileID:      int64(i + 1),
		FileDir:     dir,
		FileName:    name,
		MediaType:   classifier.MediaTypeImage,
		absPath:     dir + "/" + name,
		DBDateTaken: &dto,
		DBLat:       &lat,
		DBLon:       &lon,
		DBWidth:     new(int64(3024)),
		DBHeight:    new(int64(4032)),
		DBMake:      new("Apple"),
		DBModel:     new("iPhone 15 Pro"),
	}
}

func benchConfig(workers int) Config {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice}
	cfg.Workers = workers
	return cfg
}

// BenchmarkPlan times the whole build, geocoding included — a real vfs phase
// minus its two queries and its writes.
func BenchmarkPlan(b *testing.B) {
	geo := installtest.Resolver(b)
	cfg, log := benchConfig(8), logger.NewNoopLogger()
	for b.Loop() {
		b.StopTimer()
		masters := benchMasters(20000)
		b.StartTimer()
		if err := Plan(context.Background(), masters, cfg, geo, log); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPlanNoGeo isolates the in-memory passes — derive, cluster, merge,
// build — from the lookups. Plan tolerates a nil resolver, which is exactly
// the "everything except geocoding" case.
func BenchmarkPlanNoGeo(b *testing.B) {
	cfg, log := benchConfig(8), logger.NewNoopLogger()
	for b.Loop() {
		b.StopTimer()
		masters := benchMasters(20000)
		b.StartTimer()
		if err := Plan(context.Background(), masters, cfg, nil, log); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPlanScattered is what justifies resolveLocations' worker pool: a
// library the lookup cache can't help. Run it with -benchtime 1x — a warm
// resolver on the second iteration measures the sync.Map, not the query.
func BenchmarkPlanScattered(b *testing.B) {
	log := logger.NewNoopLogger()
	for _, workers := range []int{1, 8} {
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			// a fresh resolver per case, or the first case warms the cache
			// the second one is supposed to be paying for
			geo := installtest.Resolver(b)
			cfg := benchConfig(workers)
			for b.Loop() {
				b.StopTimer()
				masters := scatteredMasters(4000)
				b.StartTimer()
				if err := Plan(context.Background(), masters, cfg, geo, log); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
