#!/usr/bin/env bash
#
# Rebuilds frontend/public/fonts/ from upstream sources.
#
# The .woff2 files this produces ARE committed, unlike everything else this
# repository generates. A font is not derived from anything in the tree, so a
# rule that regenerated it would put a network fetch and a Python toolchain on
# the path of `npm run build` — and the output is a binary that would then differ
# between contributors for reasons no diff could explain. Committing the subset
# and committing the script that made it is the trade: the artefact is stable,
# and the recipe is auditable.
#
# Nothing runs this automatically. Run it when a face, a weight or the character
# repertoire changes, then commit what it wrote.
#
#   pip install 'fonttools[woff]' brotli     # pyftsubset, fonttools varLib
#   frontend/scripts/subset-fonts.sh
#
# Requires: bash, curl, python3 with fonttools and brotli.

set -euo pipefail

# Pinned rather than `main`. "Reproduce it" has to mean the same bytes, and
# google/fonts moves. Bump this deliberately.
readonly FONTS_REV=2d85e20401920891efb7cd6272d6339685df2820
readonly RAW="https://raw.githubusercontent.com/google/fonts/${FONTS_REV}"

readonly HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly OUT="${HERE}/../public/fonts"
readonly WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

# The repertoire. Google Fonts' own `latin` range, plus two additions this app
# needs and that range does not carry:
#
#   U+2190-2199  arrows. `→` is already in the shell, and the lanes are built
#                out of one party handing something to another.
#   U+2500-257F  box drawing. The spine is CSS, not characters — but the docs
#                draw it in text, and a code block that renders as tofu in the
#                one screenshot the design is about is not a risk worth running.
#
# Faces that have no glyph for a codepoint simply drop it; pyftsubset ignores
# missing unicodes by default.
readonly UNICODES='U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+2074,U+20AC,U+2122,U+2190-2199,U+2212,U+2215,U+2500-257F,U+FEFF,U+FFFD'

# `tnum` on top of pyftsubset's defaults, which do not include it. Figures that
# do not shift width are not a nicety here: the digest, the amounts and the poll
# counts sit in columns, and the sans counts beside them are the ones people
# forget.
readonly FEATURES='tnum'

fetch() {
  echo "  fetch $(basename "$1")"
  curl --fail --silent --show-error --location "${RAW}/$1" --output "${WORK}/$2"
}

subset() {
  local src="$1" dst="$2"
  echo "  subset ${dst}"
  pyftsubset "${WORK}/${src}" \
    --output-file="${OUT}/${dst}" \
    --flavor=woff2 \
    --unicodes="${UNICODES}" \
    --layout-features+="${FEATURES}" \
    --no-hinting
}

# Pins one weight out of a variable font. Every face this app ships is static:
# the weights are a closed set the design names, and one file per named weight
# keeps the @font-face block a list of facts rather than a range whose ends
# nobody checked.
instance() {
  local src="$1" dst="$2" axes="$3"
  echo "  instance ${dst} at ${axes}"
  python3 -m fontTools.varLib.instancer "${WORK}/${src}" ${axes} \
    --output="${WORK}/${dst}"
}

mkdir -p "${OUT}"

echo "IBM Plex Mono — the protagonist. Google Fonts ships it as statics."
fetch ofl/ibmplexmono/IBMPlexMono-Regular.ttf   plexmono-400.ttf
fetch ofl/ibmplexmono/IBMPlexMono-Medium.ttf    plexmono-500.ttf
fetch ofl/ibmplexmono/IBMPlexMono-SemiBold.ttf  plexmono-600.ttf
fetch ofl/ibmplexmono/OFL.txt                   plexmono-OFL.txt
subset plexmono-400.ttf IBMPlexMono-400.woff2
subset plexmono-500.ttf IBMPlexMono-500.woff2
subset plexmono-600.ttf IBMPlexMono-600.woff2

echo "IBM Plex Sans — support. Google Fonts ships it only as a variable font."
fetch 'ofl/ibmplexsans/IBMPlexSans%5Bwdth,wght%5D.ttf' plexsans-var.ttf
instance plexsans-var.ttf plexsans-400.ttf 'wdth=100 wght=400'
instance plexsans-var.ttf plexsans-600.ttf 'wdth=100 wght=600'
subset plexsans-400.ttf IBMPlexSans-400.woff2
subset plexsans-600.ttf IBMPlexSans-600.woff2

echo "Space Grotesk — display. Variable upstream as well."
fetch 'ofl/spacegrotesk/SpaceGrotesk%5Bwght%5D.ttf' grotesk-var.ttf
fetch ofl/spacegrotesk/OFL.txt                      grotesk-OFL.txt
instance grotesk-var.ttf grotesk-500.ttf 'wght=500'
instance grotesk-var.ttf grotesk-700.ttf 'wght=700'
subset grotesk-500.ttf SpaceGrotesk-500.woff2
subset grotesk-700.ttf SpaceGrotesk-700.woff2

# The licence travels with the files. Both families are SIL Open Font License
# 1.1; the two texts differ only in their copyright line, so both are kept.
cp "${WORK}/plexmono-OFL.txt" "${OUT}/IBMPlex-OFL.txt"
cp "${WORK}/grotesk-OFL.txt" "${OUT}/SpaceGrotesk-OFL.txt"

echo
ls -l "${OUT}"
