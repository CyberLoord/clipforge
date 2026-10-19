package media

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/CyberLoord/clipforge/internal/domain"
)

const assHeader = `[Script Info]
ScriptType: v4.00+
PlayResX: 540
PlayResY: 960
WrapStyle: 0
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,DejaVu Sans,27,&H00FFFFFF,&H00FFFFFF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,2,1,2,32,32,100,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`

func subtitles(segments []domain.Segment, clip domain.Clip) string {
	var text strings.Builder
	text.WriteString(assHeader)
	for _, segment := range segments {
		if segment.End <= clip.Start || segment.Start >= clip.End || segment.End <= segment.Start {
			continue
		}
		words := strings.Fields(segment.Text)
		if len(words) == 0 {
			continue
		}
		groups := captionGroups(words, 64)
		elapsed := 0
		for _, group := range groups {
			groupStart := segment.Start + (segment.End-segment.Start)*float64(elapsed)/float64(len(words))
			elapsed += len(group)
			groupEnd := segment.Start + (segment.End-segment.Start)*float64(elapsed)/float64(len(words))
			groupStart = math.Max(groupStart, clip.Start) - clip.Start
			groupEnd = math.Min(groupEnd, clip.End) - clip.Start
			if groupStart >= groupEnd {
				continue
			}
			if assTime(groupStart) == assTime(groupEnd) {
				continue
			}
			fmt.Fprintf(&text, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(groupStart), assTime(groupEnd), escapeASS(strings.Join(group, " ")))
		}
	}
	return text.String()
}

func captionGroups(words []string, maxChars int) [][]string {
	var groups [][]string
	start, length := 0, 0
	for i, word := range words {
		size := len([]rune(word))
		if length+size+1 > maxChars && i > start {
			groups = append(groups, words[start:i])
			start, length = i, 0
		}
		length += size + 1
	}
	return append(groups, words[start:])
}

func escapeASS(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\\':
			return '＼'
		case '{':
			return '｛'
		case '}':
			return '｝'
		default:
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}
	}, value)
}

func assTime(value float64) string {
	centiseconds := int64(math.Round(math.Max(0, value) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", centiseconds/360000, centiseconds/6000%60, centiseconds/100%60, centiseconds%100)
}
