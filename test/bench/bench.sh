#!/usr/bin/env bash
# Builds the bench from this fyne checkout with each painter and runs every scene on
# each, interleaved. With BASE set it also builds that revision, so a change gets
# measured against where it started. Everything lands in bin/.
#
#   ./bench.sh                    full matrix on the working tree, 8s per run
#   BASE=develop ./bench.sh       working tree vs develop, with a diff table at the end
#   D=3s ./bench.sh               shorter runs
#   PLATFORM=x11 ./bench.sh       force X11/XWayland (default follows the session)
#   PAINTERS=gl ./bench.sh        only some painters (default "gl wgpu-vulkan wgpu-gl")
#   TEXT=atlas ./bench.sh         only some text renderers (default "atlas slug")
#   ./bench.sh rects 5000 1       one scene/n/u instead of the matrix
set -euo pipefail
cd "$(dirname "$0")"
root=$(git rev-parse --show-toplevel)
bench=$PWD
mkdir -p bin
rm -f bin/*.log

# gl is the GL painter; wgpu-<api> the WebGPU painter on that API (vulkan, gl,
# dx12 or metal), all from the two binaries the gl and wgpu tags build.
read -ra painters <<<"${PAINTERS:-gl wgpu-vulkan wgpu-gl}"
read -ra texts <<<"${TEXT:-atlas slug}"
read -ra tags <<<"$(printf '%s\n' "${painters[@]%%-*}" | sort -u | xargs)"
revs=(head)

# A base from before the WebGPU painter would build its wgpu binary with the GL
# painter, so it only runs the painters it has.
has() { # rev painter
	[[ $1 != base || $2 != wgpu* || -d $wt/internal/painter/webgpu ]]
}

# The migrated tag drops the per-Refresh "are we on main?" check (a runtime.Stack
# call) that unmigrated apps pay, matching an app that declares the Do migration.
build() { # rev srcdir
	for p in "${tags[@]}"; do
		has "$1" "$p" || continue
		ld=""
		# goffi's dlopen stubs (gogpu loads Vulkan through it) only link in internal mode.
		[[ $p == wgpu && $(uname) == Linux ]] && ld=-linkmode=internal
		(cd "$2" && go build -tags "migrated_fynedo,$p" -ldflags "$ld" -o "$bench/bin/$1-$p" .)
	done
}

echo "building (first time takes a few minutes)..." >&2
build head .
if [[ -n ${BASE:-} ]]; then
	# A sibling worktree, so relative paths in a copied go.work still resolve.
	wt=$root/../$(basename "$root")-bench-base
	[[ -d $wt ]] || git -C "$root" worktree add -q --detach "$wt"
	git -C "$wt" checkout -qf --detach "$BASE"
	rm -rf "$wt/test/bench" "$wt"/go.work*
	mkdir -p "$wt/test/bench"
	cp ./*.go "$wt/test/bench/" # same bench code on both sides, only fyne differs
	cp "$root"/go.work* "$wt/" 2>/dev/null || true
	build base "$wt/test/bench"
	revs=(base head)
fi

# Any binary prints the header and draws the table; head has them all.
tool=bin/head-${tags[0]}

# A build is rev-painter, with -slug when text goes through Slug (FYNE_TEXT=slug)
# rather than the glyph atlas. Slug is new in head, so base only runs the atlas.
builds=()
for p in "${painters[@]}"; do
	for t in "${texts[@]}"; do
		for r in "${revs[@]}"; do
			has "$r" "$p" || continue
			suffix=""
			if [[ $t == slug ]]; then
				[[ $r == base ]] && continue
				suffix=-slug
			fi
			builds+=("$r-$p$suffix")
		done
	done
done

# Same window system for all, and no vsync blocking in present: the loop caps at
# 60 FPS on its own, so this only removes swap waits from frame times.
P=${PLATFORM:-$([ -n "${WAYLAND_DISPLAY:-}" ] && echo wayland || echo x11)}
export FYNE_PLATFORM=$P __GL_SYNC_TO_VBLANK=0 vblank_mode=0

D=${D:-8s}
out=bin/results-$P.tsv
(($#)) && out=bin/last.tsv # one-off runs don't clobber the matrix results

gpu=$(glxinfo -B 2>/dev/null | sed -n 's/.*renderer string: //p')
info="# $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | xargs), ${gpu:-unknown GPU}, $(uname -r), $P, $(go env GOVERSION)"
info+=", head $(git -C "$root" describe --always --dirty)${BASE:+, base $BASE $(git -C "$wt" rev-parse --short HEAD)}"
echo "$info" >&2

run() { # scene n u
	for b in "${builds[@]}"; do
		[[ $b == *-slug && $1 == rects ]] && continue # no text to draw
		rev=${b%%-*} painter=${b#*-}
		painter=${painter%-slug}
		env=()
		[[ $painter == wgpu-* ]] && env+=("FYNE_WGPU_BACKEND=${painter#wgpu-}")
		[[ $b == *-slug ]] && env+=(FYNE_TEXT=slug)
		bin=bin/$rev-${painter%%-*}
		echo "== $b $*" >>"bin/$b.log"
		env "${env[@]}" BENCH_T0="$(date +%s%N)" timeout 120 "$bin" -label "$b" -scene "$1" -n "$2" -u "$3" -d "$D" 2>>"bin/$b.log" ||
			printf '%s\t%s\t%s\t%s\tFAILED (see bin/%s.log)\n' "$b" "$1" "$2" "$3" "$b"
	done
}

{
	"$tool" -header
	if (($#)); then
		run "$@"
	else
		for n in 1000 5000 20000; do run rects $n 1; done
		for n in 500 2000 8000; do run text $n 1; done
		for n in 100 1000; do run zoom $n 1; done
		for n in 50 500 2000; do run widgets $n 1; done
		run widgets 500 0 # big static UI with one tile animating
	fi
} | tee "$out"

echo
echo "$info"
column -t -s $'\t' "$out"
"$tool" -table "$out" -sub "${info#\# }" && echo "table: ${out%.tsv}.png"

[[ -n ${BASE:-} ]] || exit 0
echo
awk -F'\t' '
	NR > 1 && $5 !~ /FAILED/ {
		i = index($1, "-"); k = substr($1, i + 1) "\t" $2 "\t" $3 "\t" $4
		v[substr($1, 1, i - 1), k] = $6 " " $7 " " $8 " " $11
		if (!(k in seen)) { seen[k]; keys[++nk] = k }
	}
	END {
		print "painter\tscene\tn\tu\tframe_p50_ms\tframe_p99_ms\tmain_cpu_ms\talloc_kb"
		for (j = 1; j <= nk; j++) {
			k = keys[j]
			if (!(("base", k) in v && ("head", k) in v)) continue
			split(v["base", k], x, " "); split(v["head", k], y, " ")
			line = k
			for (i = 1; i <= 4; i++)
				line = line sprintf("\t%s>%s %+.0f%%", x[i], y[i], x[i] > 0 ? 100 * (y[i] - x[i]) / x[i] : 0)
			print line
		}
	}' "$out" | tee "${out%.tsv}-diff.tsv" | column -t -s $'\t'
