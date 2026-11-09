ALTER TABLE jobs ADD COLUMN IF NOT EXISTS dispatch_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS event_sequence bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic text NOT NULL,
    message_key text NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS outbox_due_idx ON outbox (available_at, id);

CREATE TABLE IF NOT EXISTS job_events (
    id text PRIMARY KEY,
    job_id text NOT NULL,
    sequence bigint NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    UNIQUE (job_id, sequence)
);

CREATE INDEX IF NOT EXISTS job_events_job_idx ON job_events (job_id, sequence DESC);

CREATE TABLE IF NOT EXISTS dead_letters (
    id text PRIMARY KEY,
    job_id text NOT NULL DEFAULT '',
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS dead_letters_time_idx ON dead_letters (occurred_at DESC, id);

CREATE OR REPLACE FUNCTION prepare_job_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.event_sequence := 1;
        IF NEW.status = 'queued' THEN
            NEW.dispatch_generation := 1;
        END IF;
    ELSIF ROW(NEW.status, NEW.stage, NEW.attempts, NEW.error, NEW.mode)
        IS DISTINCT FROM ROW(OLD.status, OLD.stage, OLD.attempts, OLD.error, OLD.mode)
        OR OLD.event_sequence = 0 THEN
        NEW.event_sequence := OLD.event_sequence + 1;
        IF NEW.status = 'queued' AND (OLD.status <> 'queued'
            OR NEW.stage IS DISTINCT FROM OLD.stage OR NEW.mode IS DISTINCT FROM OLD.mode
            OR OLD.dispatch_generation = 0) THEN
            NEW.dispatch_generation := OLD.dispatch_generation + 1;
        END IF;
    ELSE
        NEW.event_sequence := OLD.event_sequence;
        NEW.dispatch_generation := OLD.dispatch_generation;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_job_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_id text;
    event_type text;
    destination text;
    event_time timestamptz := clock_timestamp();
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.event_sequence = OLD.event_sequence THEN
        RETURN NEW;
    END IF;
    event_id := NEW.id || ':' || NEW.event_sequence;
    destination := CASE WHEN NEW.stage = 'render' THEN 'clipforge.render.v1' ELSE 'clipforge.analysis.v1' END;
    event_type := CASE NEW.status
        WHEN 'queued' THEN 'job.queued'
        WHEN 'completed' THEN 'job.completed'
        WHEN 'failed' THEN 'job.failed'
        WHEN 'cancelled' THEN 'job.cancelled'
        ELSE 'stage.changed'
    END;
    IF NEW.status = 'running' AND (TG_OP = 'INSERT' OR OLD.status <> 'running') THEN
        event_type := 'stage.started';
    END IF;
    INSERT INTO outbox (topic, message_key, payload) VALUES (
        'clipforge.events.v1', NEW.id,
        jsonb_build_object('id', event_id, 'job_id', NEW.id, 'sequence', NEW.event_sequence,
            'type', event_type, 'status', NEW.status, 'stage', NEW.stage,
            'attempt', NEW.attempts, 'error', NEW.error, 'occurred_at', event_time)
    );
    IF NEW.status = 'queued' AND (TG_OP = 'INSERT' OR NEW.dispatch_generation <> OLD.dispatch_generation) THEN
        INSERT INTO outbox (topic, message_key, payload, available_at) VALUES (
            destination, NEW.id,
            jsonb_build_object('id', NEW.id || ':' || NEW.dispatch_generation, 'job_id', NEW.id,
                'generation', NEW.dispatch_generation, 'stage', NEW.stage),
            NEW.available_at
        );
    END IF;
    IF NEW.status = 'failed' THEN
        INSERT INTO outbox (topic, message_key, payload) VALUES (
            'clipforge.dlq.v1', NEW.id,
            jsonb_build_object('id', event_id || ':failed', 'job_id', NEW.id, 'kind', 'job_failed',
                'error', NEW.error, 'source_topic', destination, 'partition', -1, 'offset', -1,
                'occurred_at', event_time)
        );
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS jobs_prepare_event ON jobs;
CREATE TRIGGER jobs_prepare_event BEFORE INSERT OR UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION prepare_job_event();

DROP TRIGGER IF EXISTS jobs_enqueue_event ON jobs;
CREATE TRIGGER jobs_enqueue_event AFTER INSERT OR UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION enqueue_job_event();

UPDATE jobs SET status = 'queued', lease_until = NULL,
    available_at = CASE WHEN status = 'running' THEN now() ELSE available_at END,
    updated_at = now()
WHERE status IN ('queued', 'running') AND dispatch_generation = 0;
