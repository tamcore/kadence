---
name: workout-programming
description: Build proper, specifically-typed structured workouts instead of generic steps.
triggers:
  - "*_workout"
  - "*create*workout*"
  - "*schedule*workout*"
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
