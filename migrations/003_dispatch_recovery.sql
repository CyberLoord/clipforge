ALTER TABLE jobs ADD COLUMN IF NOT EXISTS dispatched_at timestamptz;

CREATE INDEX IF NOT EXISTS jobs_dispatch_idx ON jobs (dispatched_at) WHERE status = 'queued';
CREATE UNIQUE INDEX IF NOT EXISTS outbox_command_idx ON outbox (message_key, (payload->>'generation'))
    WHERE topic IN ('clipforge.analysis.v1', 'clipforge.render.v1');

CREATE OR REPLACE FUNCTION prepare_job_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.event_sequence := 1;
        IF NEW.status = 'queued' THEN
            NEW.dispatch_generation := 1;
            NEW.dispatched_at := NULL;
        END IF;
    ELSIF ROW(NEW.status, NEW.stage, NEW.attempts, NEW.error, NEW.mode)
        IS DISTINCT FROM ROW(OLD.status, OLD.stage, OLD.attempts, OLD.error, OLD.mode)
        OR OLD.event_sequence = 0 THEN
        NEW.event_sequence := OLD.event_sequence + 1;
        IF NEW.status = 'queued' AND (OLD.status <> 'queued'
            OR NEW.stage IS DISTINCT FROM OLD.stage OR NEW.mode IS DISTINCT FROM OLD.mode
            OR OLD.dispatch_generation = 0) THEN
            NEW.dispatch_generation := OLD.dispatch_generation + 1;
            NEW.dispatched_at := NULL;
        END IF;
    ELSE
        NEW.event_sequence := OLD.event_sequence;
        NEW.dispatch_generation := OLD.dispatch_generation;
    END IF;
    RETURN NEW;
END;
$$;

UPDATE jobs j SET dispatched_at = now()
WHERE status = 'queued' AND dispatched_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.message_key = j.id
        AND o.topic IN ('clipforge.analysis.v1', 'clipforge.render.v1')
        AND o.payload->>'generation' = j.dispatch_generation::text);
