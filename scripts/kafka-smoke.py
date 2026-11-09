import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

base = os.environ.get('BASE_URL', 'http://127.0.0.1:8080').rstrip('/')
ffmpeg = os.environ.get('FFMPEG_PATH', 'ffmpeg')


def request(path, data=None, headers=None):
    req = urllib.request.Request(base + path, data=data, headers=headers or {})
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.load(response)


def upload(path):
    boundary = uuid.uuid4().hex
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="mode"\r\n\r\nsplit\r\n'
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{path.name}"\r\n'
            'Content-Type: video/mp4\r\n\r\n').encode() + path.read_bytes() + f'\r\n--{boundary}--\r\n'.encode()
    return request('/api/jobs', body, {'Content-Type': f'multipart/form-data; boundary={boundary}'})


def until(check, description):
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(.5)
    raise TimeoutError(description)


def finished(job_id, status):
    job = request(f'/api/jobs/{job_id}')
    if job['status'] == status:
        return job
    if job['status'] in ('failed', 'cancelled', 'completed'):
        raise AssertionError(f'Unexpected job result: {job["status"]}: {job["error"]}')


def final_history(job_id, status):
    events = request(f'/api/jobs/{job_id}/events')
    if not events or events[-1]['status'] != status:
        return None
    sequences = [event['sequence'] for event in events]
    assert sequences == sorted(set(sequences)), events
    return events


with tempfile.TemporaryDirectory(prefix='clipforge-kafka-') as directory:
    source = Path(directory) / 'outage.mp4'
    subprocess.run([ffmpeg, '-nostdin', '-v', 'error', '-y', '-f', 'lavfi', '-i',
                    'testsrc2=size=320x180:rate=15', '-t', '15', '-c:v', 'libx264',
                    '-threads', '2', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', str(source)], check=True)
    subprocess.run(['docker', 'compose', 'stop', 'kafka'], check=True)
    try:
        job = upload(source)
        time.sleep(2)
        queued = request(f'/api/jobs/{job["id"]}')
        assert queued['status'] == 'queued', queued
        print('Upload accepted while Kafka is stopped; job remains queued.', flush=True)
    finally:
        subprocess.run(['docker', 'compose', 'start', '--wait', 'kafka'], check=True)
    completed = until(lambda: finished(job['id'], 'completed'), 'job did not resume after Kafka restart')
    assert len(completed['clips']) == 3, completed
    events = until(lambda: final_history(job['id'], 'completed'), 'history projection did not catch up')
    assert any(event['stage'] == 'render' and event['status'] == 'queued' for event in events), events
    print('Kafka recovered: analysis → render → completed, history projected.', flush=True)
    before = [event['id'] for event in events]
    subprocess.run(['docker', 'compose', 'stop', 'events'], check=True)
    try:
        subprocess.run(['docker', 'compose', 'exec', '-T', 'kafka',
                        '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9092',
                        '--group', 'clipforge-history', '--topic', 'clipforge.events.v1',
                        '--reset-offsets', '--to-earliest', '--execute'], check=True)
    finally:
        subprocess.run(['docker', 'compose', 'start', '--wait', 'events'], check=True)

    def history_caught_up():
        output = subprocess.check_output(['docker', 'compose', 'exec', '-T', 'kafka',
                                          '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9092',
                                          '--group', 'clipforge-history', '--describe'], text=True)
        rows = [line.split() for line in output.splitlines()
                if line.strip().startswith('clipforge-history ') and 'clipforge.events.v1' in line]
        return len(rows) == 3 and all(row[5] == '0' for row in rows)

    until(history_caught_up, 'history consumer did not catch up after replay')
    replayed = request(f'/api/jobs/{job["id"]}/events')
    assert [event['id'] for event in replayed] == before, replayed
    after = request(f'/api/jobs/{job["id"]}')
    assert after['clips'] == completed['clips'] and after['updated_at'] == completed['updated_at'], after
    print('History replay caught up without duplicate events or another render.', flush=True)
    invalid = Path(directory) / 'too-short.mp4'
    subprocess.run([ffmpeg, '-nostdin', '-v', 'error', '-y', '-i', str(source),
                    '-t', '3', '-c', 'copy', str(invalid)], check=True)
    failed = upload(invalid)
    until(lambda: finished(failed['id'], 'failed'), 'invalid video was not rejected')
    until(lambda: final_history(failed['id'], 'failed'), 'failed event did not arrive')
    letters = until(lambda: [letter for letter in request('/api/dead-letters')
                            if letter.get('job_id') == failed['id']], 'terminal failure did not reach DLQ')
    assert letters[0]['kind'] == 'job_failed', letters
    print('Terminal failure delivered to DLQ and diagnostics. Kafka smoke passed.', flush=True)
