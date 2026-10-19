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
ffprobe = os.environ.get('FFPROBE_PATH', 'ffprobe')


def request(path, data=None, method=None, headers=None, expected=200):
    req = urllib.request.Request(base + path, data=data, method=method, headers=headers or {})
    try:
        response = urllib.request.urlopen(req, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        payload = response.read()
        if response.status != expected:
            raise RuntimeError(f'{path}: HTTP {response.status}: {payload[:1000]!r}')
        return payload


def upload(filename, content, expected=202):
    boundary = uuid.uuid4().hex
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
            'Content-Type: video/mp4\r\n\r\n').encode() + content + f'\r\n--{boundary}--\r\n'.encode()
    return json.loads(request('/api/jobs', body, 'POST', {'Content-Type': f'multipart/form-data; boundary={boundary}'}, expected))


def wait_job(job_id):
    deadline = time.monotonic() + 180
    last = None
    while time.monotonic() < deadline:
        job = json.loads(request(f'/api/jobs/{job_id}'))
        state = (job['status'], job['stage'])
        if state != last:
            print(job_id, *state, flush=True)
            last = state
        if job['status'] == 'completed':
            return job
        if job['status'] in ('failed', 'cancelled'):
            raise RuntimeError(job['error'] or job['status'])
        time.sleep(0.3)
    raise TimeoutError(f'job {job_id} did not finish')


def verify_video(path, expected_duration):
    output = subprocess.check_output([ffprobe, '-v', 'error', '-show_entries',
                                      'format=duration:stream=codec_type,codec_name,width,height,pix_fmt',
                                      '-of', 'json', str(path)])
    info = json.loads(output)
    video = next(stream for stream in info['streams'] if stream['codec_type'] == 'video')
    audio = next(stream for stream in info['streams'] if stream['codec_type'] == 'audio')
    assert (video['width'], video['height']) == (540, 960), video
    assert video['codec_name'] == 'h264' and video['pix_fmt'] == 'yuv420p', video
    assert audio['codec_name'] == 'aac', audio
    assert abs(float(info['format']['duration']) - expected_duration) < 0.2, info


config = json.loads(request('/api/config'))
if config['mode'] != 'demo':
    raise RuntimeError('smoke test requires AI_MODE=demo')
with tempfile.TemporaryDirectory(prefix='clipforge-smoke-') as directory:
    root = Path(directory)
    source = root / 'source.mp4'
    subprocess.run([ffmpeg, '-nostdin', '-v', 'error', '-y', '-f', 'lavfi', '-i',
                    'testsrc2=size=640x360:rate=24', '-f', 'lavfi', '-i',
                    'sine=frequency=440:sample_rate=44100', '-t', '65', '-c:v', 'libx264',
                    '-threads', '2', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p',
                    '-c:a', 'aac', '-movflags', '+faststart', str(source)], check=True)
    upload('bad.txt', b'not a video', expected=415)
    upload('bad.mp4', b'not a video at all', expected=415)
    job = wait_job(upload('smoke.mp4', source.read_bytes())['id'])
    assert len(job['clips']) == 3 and job['transcript'], job
    for clip in job['clips']:
        path = root / f"{clip['id']}.mp4"
        endpoint = f"/api/jobs/{job['id']}/clips/{clip['id']}/video"
        path.write_bytes(request(endpoint + '?download=1'))
        verify_video(path, clip['end'] - clip['start'])
        assert len(request(endpoint, headers={'Range': 'bytes=0-31'}, expected=206)) == 32
    clip = job['clips'][0]
    old_paths = [c['path'] for c in job['clips']]
    request(f"/api/jobs/{job['id']}/clips/{clip['id']}", json.dumps({'start': 0, 'end': 5}).encode(),
            'PATCH', {'Content-Type': 'application/json'}, expected=400)
    request(f"/api/jobs/{job['id']}/clips/{clip['id']}", json.dumps({'start': 1, 'end': 21}).encode(),
            'PATCH', {'Content-Type': 'application/json'}, expected=202)
    updated = wait_job(job['id'])
    assert updated['transcript'] == job['transcript']
    assert updated['clips'][0]['revision'] == clip['revision'] + 1
    assert updated['clips'][0]['path'] != old_paths[0]
    assert [c['path'] for c in updated['clips'][1:]] == old_paths[1:]
    edited = root / 'edited.mp4'
    edited.write_bytes(request(f"/api/jobs/{job['id']}/clips/{clip['id']}/video"))
    verify_video(edited, 20)
    second = upload('cancel.mp4', source.read_bytes())
    request(f"/api/jobs/{second['id']}/cancel", b'', 'POST')
    cancelled = json.loads(request(f"/api/jobs/{second['id']}"))
    assert cancelled['status'] == 'cancelled'
    request(f"/api/jobs/{second['id']}/retry", b'', 'POST')
    wait_job(second['id'])
    print(f'Smoke passed: three clips, re-export, byte ranges, cancel/retry. {base}/#{job["id"]}')
