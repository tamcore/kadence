---
name: pace-conversion
description: Use when converting running pace units or preparing numeric workout pace targets.
triggers:
  - "kadence__convert_pace"
---
Use `kadence__convert_pace` for every running pace conversion. Do not calculate
or approximate conversions yourself.

- `unit: metric` means the input is minutes per kilometer.
- `unit: imperial` means the input is minutes per mile.
- `targetpace` must use `M:SS`.
- Choose `output: metric` for min/km, `imperial` for min/mi, or `mps` for
  meters per second.
- Make one tool call per pace. A pace range needs one call for each bound.
- Use the returned value directly without recalculating or rounding it.

For workout `pace.zone` ranges, assign the faster, higher meters-per-second
value to `targetValueOne` and the slower, lower value to `targetValueTwo`.
