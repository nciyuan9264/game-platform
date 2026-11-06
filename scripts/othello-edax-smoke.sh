#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: othello-edax-smoke.sh <roomhost-othello-image>}"
eval_sha256="f8b2299612d9fa4414157e70e932636e33111c2602d0c2fc382a7d90ef21b792"

docker run --rm --entrypoint sh "$image" -ceu "
  test -x /opt/edax/bin/edax
  test -f /opt/edax/data/eval.dat
  test -f /opt/edax/LICENSE
  test -f /opt/edax/source/edax-reversi-v4.7.tar.gz
  echo '${eval_sha256}  /opt/edax/data/eval.dat' | sha256sum -c -

  version=\"\$(printf 'version\\n' |
    timeout 5 /opt/edax/bin/edax -gtp \
      -eval-file /opt/edax/data/eval.dat \
      -book-file /tmp/edax-analysis-book.dat \
      -book-usage off \
      -hash-table-size 18 \
      -n-tasks 1)\"
  printf '%s\\n' \"\$version\" | grep -F '4.7'

  analysis=\"\$(printf 'nboard 1\\nhint 1\\n' |
    timeout 8 /opt/edax/bin/edax -nboard -q \
      -eval-file /opt/edax/data/eval.dat \
      -book-file /tmp/edax-analysis-book.dat \
      -book-usage off \
      -ponder off \
      -move-time 1 \
      -hash-table-size 18 \
      -n-tasks 1)\"
  printf '%s\\n' \"\$analysis\"
  printf '%s\\n' \"\$analysis\" |
    awk '/^search / {
      if (\$(NF-1) == 0 && \$NF >= 0 && \$NF <= 60) found = 1
    } END { exit found ? 0 : 1 }'
"

docker run --rm "$image" self-test
