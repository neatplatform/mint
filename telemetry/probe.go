package telemetry

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Probe is the central observability handle, combining a logger, meter, and tracer under a single interface.
//
// It also implements [http.Handler] to serve the Prometheus metrics endpoint when Prometheus is enabled.
type Probe interface {
	http.Handler

	Info() (name string, version string)
	Logger() Logger
	Meter() metric.Meter
	Tracer() trace.Tracer
	Close(context.Context) error
}

type probe struct {
	name, version string
	logger        Logger
	meter         metric.Meter
	tracer        trace.Tracer
	closeFuncs    []closeFunc
	promHandler   http.Handler
}

func (p *probe) Info() (string, string) {
	return p.name, p.version
}

func (p *probe) Logger() Logger {
	return p.logger
}

func (p *probe) Meter() metric.Meter {
	return p.meter
}

func (p *probe) Tracer() trace.Tracer {
	return p.tracer
}

func (p *probe) Close(ctx context.Context) error {
	var err error

	if e := p.logger.Close(); e != nil {
		err = errors.Join(err, e)
	}

	for _, close := range p.closeFuncs {
		if e := close(ctx); e != nil {
			err = errors.Join(err, e)
		}
	}

	return err
}

// ServeHTTP implements [http.Handler], serving the Prometheus metrics endpoint when Prometheus is enabled.
func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.promHandler != nil {
		p.promHandler.ServeHTTP(w, r)
	}
}

// NewNoopProbe returns a [Probe] whose logger, meter, and tracer are all no-ops.
//
// It is useful for testing or for disabling telemetry without changing the code that uses the probe.
func NewNoopProbe() Probe {
	return &probe{
		logger: new(noopLogger),
		meter:  metricnoop.NewMeterProvider().Meter(""),
		tracer: tracenoop.NewTracerProvider().Tracer(""),
	}
}

// NewProbe returns a configured [Probe].
// It seeds defaults from environment variables and then applies any provided [Option]s on top.
func NewProbe(opts ...Option) Probe {
	// Seed options from environment variables and apply given options on top.
	o := optionsFromEnv()
	for _, opt := range opts {
		opt(&o)
	}

	p := &probe{}

	if o.Metadata.Name != "" {
		p.name = o.Metadata.Name
	}

	if o.Metadata.Version != "" {
		p.version = o.Metadata.Version
	}

	loggers := []Logger{}

	if opts := o.Logger.Stdout; opts != nil {
		loggers = append(loggers, newStdoutLogger(*opts, o.Metadata))
	}

	if opts := o.Logger.File; opts != nil {
		l, err := newFileLogger(*opts, o.Metadata)
		if err != nil {
			panic(err)
		}

		loggers = append(loggers, l)
	}

	if opts := o.Logger.Loki; opts != nil {
		loggers = append(loggers, newLokiLogger(*opts, o.Metadata))
	}

	if opts := o.Logger.Forward; opts != nil {
		l, err := newForwardLogger(*opts, o.Metadata)
		if err != nil {
			panic(err)
		}

		loggers = append(loggers, l)
	}

	if opts := o.Logger.OpenTelemetry; opts != nil {
		loggers = append(loggers, newOpenTelemetryLogger(*opts, o.Metadata))
	}

	if opts := o.Meter.OpenTelemetry; opts != nil {
		meter, close, handler := createOTelMeter(*opts, o.Metadata)
		p.meter, p.closeFuncs, p.promHandler = meter, append(p.closeFuncs, close), handler
	}

	if opts := o.Tracer.OpenTelemetry; opts != nil {
		tracer, close := createOTelTracer(*opts, o.Metadata)
		p.tracer, p.closeFuncs = tracer, append(p.closeFuncs, close)
	}

	/* Ensure logger, meter, and tracer are initialized to avoid nil pointer dereferences in user code. */

	switch len(loggers) {
	case 0:
		p.logger = new(noopLogger)
	case 1:
		p.logger = loggers[0]
	default:
		p.logger = newMultiLogger(loggers...)
	}

	if p.meter == nil {
		p.meter = metricnoop.NewMeterProvider().Meter("")
	}

	if p.tracer == nil {
		p.tracer = tracenoop.NewTracerProvider().Tracer("")
	}

	return p
}
