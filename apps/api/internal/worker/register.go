package worker

// Registration hooks for work packages (docs-parity-b 13.4). wire.go is the lead's file; a package that adds an event
// consumer, a job or a sweep registers it from an init() in its own file, so two packages never edit the same lines:
//
//	func init() { RegisterEvent("note.updated", tasksOnNoteEvent) }
//
// Registered consumers run after the ones wire.go lists, in registration order (init order is by file name within the
// package, so a consumer that must come before another is registered from wire.go instead).
var (
	extraEvents []eventHandler
	extraJobs   = map[string]JobHandler{}
	extraSweeps []Sweep
)

func RegisterEvent(prefix string, h EventHandler) {
	extraEvents = append(extraEvents, eventHandler{prefix, h})
}
func RegisterJob(kind string, h JobHandler) { extraJobs[kind] = h }
func RegisterSweep(s Sweep)                 { extraSweeps = append(extraSweeps, s) }

func (w *Worker) addRegistered() {
	for _, e := range extraEvents {
		w.OnEvent(e.prefix, e.h)
	}
	for k, h := range extraJobs {
		w.OnJob(k, h)
	}
	for _, s := range extraSweeps {
		w.AddSweep(s)
	}
}
