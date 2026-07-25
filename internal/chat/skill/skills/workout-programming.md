---
name: workout-programming
description: Build proper, specifically-typed structured workouts instead of generic steps.
triggers:
  # Only workout CREATION/edit tools — never reads or scheduling. A broader
  # pattern like "*schedule*workout*" wrongly matched get_scheduled_workouts
  # (a read), pre-gating it so the calendar was never fetched.
  - "*create_*_workout"
  - "*update_workout"
  - "*upload_workout*"
---
When you create or edit a structured workout through a tool, build a proper,
specific workout of the correct type.

- Choose the builder tool that matches the activity (a run builder for runs, a
  strength builder for strength, and so on). Never force one activity type into
  another builder.
- Never fill a workout with generic, unnamed, or placeholder steps.
- If a tool exposes a catalog of valid exercise or step types, call that catalog
  tool FIRST and set every step to a specific entry from it, using the exact
  identifier the catalog returns. Free-text or approximate exercise names are
  commonly downgraded by the tool to a single generic step, which is wrong.
- Give each strength exercise concrete sets, reps, and rest. Give each run or
  interval step a concrete duration or distance and a target.
- After creating a workout, confirm it actually contains the intended,
  specifically-typed exercises before telling the user it is done.

For `upload_workout` and `update_workout`, use the complete structured workout
DTO. `update_workout` replaces the whole workout: fetch the current workout
first, apply the requested edits, and submit the complete name, sport, segments,
and steps rather than a partial patch.

For running `ExecutableStepDTO` steps, encode the step's actual intent:

- Explicit warmup: `{"stepTypeId": 1, "stepTypeKey": "warmup"}`.
- Explicit cooldown: `{"stepTypeId": 2, "stepTypeKey": "cooldown"}`. A terminal
  step labeled cooldown must not be typed as recovery. An explicit lap-button
  cooldown uses end condition `{"conditionTypeId": 1, "conditionTypeKey": "lap.button"}`.
- Ordinary running and work intervals: `{"stepTypeId": 3, "stepTypeKey": "interval"}`.
  This is the Run step type.
- Active recovery jog: `{"stepTypeId": 4, "stepTypeKey": "recovery"}`.
- Walking or complete rest: `{"stepTypeId": 5, "stepTypeKey": "rest"}`.

For custom numeric running pace ranges, use
`{"workoutTargetTypeId": 6, "workoutTargetTypeKey": "pace.zone"}` and do not use
`zoneNumber`.

Call `kadence__convert_pace` with `output: mps` once for every pace bound. Use
the user's source unit (`metric` for min/km or `imperial` for min/mi) and use
each returned number directly. Do not calculate, approximate, or round the
conversion yourself.

Set `targetValueOne` to the faster bound returned by the converter (the higher
meters-per-second value) and `targetValueTwo` to the slower bound (the lower
meters-per-second value).

Repeated intervals use `{"type": "RepeatGroupDTO"}` with
`numberOfIterations` and an end condition containing both
`{"conditionTypeId": 7, "conditionTypeKey": "iterations"}`. Apply the same
Run, recovery, and rest distinctions to its nested steps.
