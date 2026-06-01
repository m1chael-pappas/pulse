// Package system is a Provider that surfaces local machine performance:
// CPU, memory, disk, load average, and (on macOS) battery state.
package system

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/michaelpappas/pulse/internal/providers"
)

type Provider struct {
	// DiskPath is the mount point to track (defaults to /, which on macOS
	// is the data volume).
	DiskPath string

	// cpuSampler keeps a one-second moving window so % CPU isn't 0 the
	// first time we ask. gopsutil requires a delta between calls.
	cpuPrime bool
}

func New() *Provider { return &Provider{DiskPath: "/"} }

func (p *Provider) Name() string             { return "System" }
func (p *Provider) Interval() time.Duration  { return 3 * time.Second }
func (p *Provider) PreferredWidth() int      { return 48 }

func (p *Provider) Refresh(ctx context.Context) providers.Snapshot {
	snap := providers.Snapshot{
		Name:   p.Name(),
		Status: providers.StatusOK,
	}

	// Prime the cpu sampler on the first call so subsequent ones return
	// a meaningful delta. gopsutil's cpu.Percent with a 0 interval
	// returns the value since the last call.
	if !p.cpuPrime {
		_, _ = cpu.PercentWithContext(ctx, 0, false)
		p.cpuPrime = true
	}

	cpuPct := cpuUsage(ctx)
	memInfo, memErr := mem.VirtualMemoryWithContext(ctx)
	diskInfo, diskErr := disk.UsageWithContext(ctx, p.DiskPath)
	loadInfo, _ := load.AvgWithContext(ctx)

	if memErr != nil || diskErr != nil {
		snap.Status = providers.StatusWarn
	}

	cores, _ := cpu.CountsWithContext(ctx, true)
	if cores == 0 {
		cores = runtime.NumCPU()
	}

	snap.Header = fmt.Sprintf("%d cores", cores)
	if h, err := host.InfoWithContext(ctx); err == nil && h.Hostname != "" {
		snap.Name = "System — " + h.Hostname
	}

	windows := []providers.Window{
		{
			Label: "CPU",
			Used:  cpuPct,
			Limit: 100,
			Unit:  providers.UnitPercent,
		},
	}
	if memInfo != nil {
		windows = append(windows, providers.Window{
			Label: "Memory",
			Used:  bytesToGiB(memInfo.Used),
			Limit: bytesToGiB(memInfo.Total),
			Unit:  providers.UnitGiB,
		})
	}
	if diskInfo != nil {
		windows = append(windows, providers.Window{
			Label: "Disk " + p.DiskPath,
			Used:  bytesToGiB(diskInfo.Used),
			Limit: bytesToGiB(diskInfo.Total),
			Unit:  providers.UnitGiB,
		})
	}
	snap.Windows = windows

	stats := []providers.BreakdownEntry{
		{Label: "Load 1m", Value: loadValue(loadInfo, 1)},
		{Label: "Load 5m", Value: loadValue(loadInfo, 5)},
		{Label: "Load 15m", Value: loadValue(loadInfo, 15)},
		{Label: "Cores", Value: float64(cores)},
	}
	if loadInfo != nil && loadInfo.Load1 > float64(cores)*0.8 {
		snap.Status = providers.StatusWarn
	}
	if loadInfo != nil && loadInfo.Load1 > float64(cores) {
		snap.Status = providers.StatusIncident
	}
	snap.Stats = stats

	// Hottest sensor reading (macOS exposes a handful; Linux varies).
	if temps, err := sensors.TemperaturesWithContext(ctx); err == nil && len(temps) > 0 {
		var hottest float64
		var label string
		for _, t := range temps {
			if t.Temperature > hottest {
				hottest = t.Temperature
				label = t.SensorKey
			}
		}
		if hottest > 0 {
			snap.Subtitle = fmt.Sprintf("hottest sensor: %s %.0f°C", trimSensorKey(label), hottest)
		}
	}

	return snap
}

func cpuUsage(ctx context.Context) float64 {
	pcts, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil || len(pcts) == 0 {
		return 0
	}
	return pcts[0]
}

func loadValue(l *load.AvgStat, window int) float64 {
	if l == nil {
		return 0
	}
	switch window {
	case 1:
		return l.Load1
	case 5:
		return l.Load5
	case 15:
		return l.Load15
	}
	return 0
}

func bytesToGiB(b uint64) float64 {
	return float64(b) / (1024 * 1024 * 1024)
}

func trimSensorKey(k string) string {
	// macOS keys look like "TC0P" / "Tp09"; cut anything before the first
	// alphanumeric for readability and drop trailing markers.
	k = strings.TrimSpace(k)
	if len(k) > 12 {
		k = k[:12]
	}
	return k
}

