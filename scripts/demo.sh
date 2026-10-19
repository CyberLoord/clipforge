set -eu
cd "$(dirname "$0")/.."
mkdir -p data/demo
"${FFMPEG_PATH:-ffmpeg}" -nostdin -hide_banner -loglevel error -y \
  -f lavfi -i 'testsrc2=size=640x360:rate=24' \
  -f lavfi -i 'sine=frequency=440:sample_rate=44100' \
  -t 65 -c:v libx264 -threads 2 -preset ultrafast -pix_fmt yuv420p \
  -c:a aac -movflags +faststart data/demo/sample.mp4
printf '%s\n' 'Upload data/demo/sample.mp4 and select the demo mode in the browser.'
