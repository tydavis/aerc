//go:build darwin
// +build darwin

package darwin

import (
	"time"

	"git.sr.ht/~rjarry/aerc/log"
	"git.sr.ht/~rjarry/aerc/worker/handlers"
	"git.sr.ht/~rjarry/aerc/worker/types"
	"github.com/fsnotify/fsevents"
)

func init() {
	handlers.RegisterWatcherFactory("darwin", newDarwinWatcher)
}

type darwinWatcher struct {
	ch        chan *types.FSEvent
	w         *fsevents.EventStream
	watcherCh chan []fsevents.Event
}

func newDarwinWatcher() (types.FSWatcher, error) {
	watcher := &darwinWatcher{
		watcherCh: make(chan []fsevents.Event),
		ch:        make(chan *types.FSEvent),
		w: &fsevents.EventStream{
			Flags:   fsevents.FileEvents | fsevents.WatchRoot,
			Latency: 500 * time.Millisecond,
		},
	}
	return watcher, nil
}

func (w *darwinWatcher) watch() {
	defer log.PanicHandler()
	for events := range w.w.Events {
		for _, ev := range events {
			switch {
			case ev.Flags&fsevents.ItemCreated > 0:
				w.ch <- &types.FSEvent{
					Operation: types.FSCreate,
					Path:      ev.Path,
				}
			case ev.Flags&fsevents.ItemRenamed > 0:
				w.ch <- &types.FSEvent{
					Operation: types.FSRename,
					Path:      ev.Path,
				}
			case ev.Flags&fsevents.ItemRemoved > 0:
				w.ch <- &types.FSEvent{
					Operation: types.FSRemove,
					Path:      ev.Path,
				}
			}
		}
	}
}

func (w *darwinWatcher) Configure(root string) error {
	dev, err := fsevents.DeviceForPath(root)
	if err != nil {
		return err
	}
	w.w.Device = dev
	w.w.Paths = []string{root}
	w.w.Start()
	go w.watch()
	return nil
}

func (w *darwinWatcher) Events() chan *types.FSEvent {
	return w.ch
}

func (w *darwinWatcher) Add(p string) error {
	return nil
}

func (w *darwinWatcher) Remove(p string) error {
	return nil
}
