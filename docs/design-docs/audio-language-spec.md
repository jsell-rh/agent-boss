# Audio Language Spec — OpenDispatch (TASK-062)

**Status:** Draft — Pending review by audio-sme and cto before implementation
**Author:** ux
**Date:** 2026-03-16

---

## Problem

OpenDispatch currently plays sounds that all live in the same perceptual register: pentatonic
tones, sine sweeps, chords. A user cannot reliably answer:

- *What happened?* (action)
- *Who caused it?* (sender identity)
- *Who was it directed at?* (recipient identity)

When every sound is a variation of "soft tone + decay," the signal collapses into audio noise.
The goal of this spec is a **compositional grammar** that makes the answer to each of those
questions immediately audible — without requiring the user to consciously parse anything.

---

## Design Principle: Three Perceptual Layers

Every sound event in OpenDispatch should be decomposable into at most three layers, each occupying
a distinct perceptual register:

```
Sound = [ACTION_CUE] + [pause 80-150ms] + [SENDER_VOICE?] + [pause 40ms] + [RECIPIENT_VOICE?]
```

| Layer | What it conveys | Perceptual signature |
|-------|-----------------|----------------------|
| **Action cue** | *What happened* | Rhythmic, textural — sweeps, noise bursts, dissonance |
| **Sender voice** | *Who did it* | Melodic, pentatonic, agent-unique (5D system) |
| **Recipient voice** | *Who received it* | Same as sender but 50% softer + panned toward recipient's position |

**Why separate registers matter:** Action cues are structural/textural; identity voices are
melodic/harmonic. The brain processes these in parallel streams (auditory scene analysis). A
user learns "that rhythmic knock = task moved" and "that rising chord = arch2" independently.
They can then combine: "knocking sound + arch2's voice = arch2 moved a task." No conscious
parsing required.

---

## Layer 1: Action Cue Library

Action cues must be **immediately identifiable by shape**, not by pitch. Every cue is defined
by its rhythm and texture, not its frequency, so the cue stays recognizable across all sound
themes.

| Event | Cue shape | Duration | Category |
|-------|-----------|----------|----------|
| Agent spawned | Rising warp sweep (80→2000Hz linear) | 300ms | events |
| Task → in_progress | Single rising step (low→mid, 80ms) | 150ms | events |
| Task → review | Suspended two-note (root + maj2, 300ms held) | 350ms | events |
| Task → done | Ascending 3-note resolution (C→E→G arpeggio) | 350ms | celebrations |
| Task (critical) → done | Same + head run 400ms before | 750ms | celebrations |
| Sprint complete | Full ascending run (7 notes) | 700ms | celebrations |
| Message sent/received | Two-beat knock (80ms, 80ms gap, 60ms) | 220ms | social |
| @mention received | Single high percussive ping (< 100ms) | 100ms | social / urgent |
| Agent blocked/error | Descending tritone + 7Hz tremolo | 500ms | urgent |
| PR link set | Descending whoosh (high→low) | 250ms | events |
| Agent idle | Falling step (reverse of in_progress) | 100ms | ambient |
| Activity tick | 4ms white-noise burst | 4ms | ambient |

**Cue design rules:**
1. Ascending = progress / starting / positive
2. Descending = finishing / shipping / settling
3. Dissonant (tritone, minor 2nd) = problem / needs attention
4. Rhythmic (double-beat) = communication event
5. Single percussive ping = direct address (mention)

---

## Layer 2: Sender Identity Voice

The 5D agent voice system (already implemented):
- Waveform × Interval × Envelope × Register × Rhythm = 480 distinct voices
- Palette guarantee: first 16 agents in a space get unique (waveform, interval) combos
- Stereo panning: each agent has a consistent left/right position
- Micro-variation: ±8 cents drift + timing humanization per play

**When sender identity is included:**
- Agent spawned: voice plays immediately after warp cue
- Message sent by agent: voice plays after the two-beat knock
- @mention: voice plays after the ping (so you know WHO mentioned you)
- Agent status change (active/idle): voice plays the mood variant (ascending/descending)
- Task done: optionally include voice after success chord (configurable: "announce assignee")

**When to omit sender identity:**
- Ambient ticks: too frequent, voice would be fatiguing
- PR shipped: action cue is self-contained
- Sprint complete: celebration sound speaks for itself

---

## Layer 3: Recipient Identity Voice

Only for direct communication events:
- **@mention**: recipient voice plays 40ms after sender voice, 50% softer, opposite pan
- **Agent→agent message**: same as @mention treatment

This creates a brief "conversation harmony" — two agent voices overlapping slightly —
that feels like an audio representation of one agent calling to another.

---

## Urgency Markers

Three tiers, distinguishable before even processing the action cue:

| Tier | Marker | Placement |
|------|--------|-----------|
| **Urgent (L3)** | Fast attack (< 2ms), full volume, center-panned | Override all other pan; repeat once after 350ms if agent stays blocked |
| **Events (L1)** | Normal attack (20ms), standard volume | No marker |
| **Celebrations (L2)** | Ascending 2-note prefix before action cue | Prefix plays 150ms before main cue |
| **Ambient (L0)** | No cue structure — micro-tone burst only | No identity layers added |

---

## Distinguishability: Action vs Identity — Design Rules

The current system blurs action and identity because both use the same sine/triangle
oscillators at pentatonic frequencies. To fix this structurally:

**Action cues MUST use at least one of:**
- Frequency sweep (not a static tone)
- Noise component (filtered white noise for texture)
- Dissonant interval (tritone or minor 2nd — never used in identity voices)
- Percussive attack with < 5ms onset (sharper than any identity voice)

**Identity voices MUST use:**
- Pentatonic consonant frequencies only
- Gradual attack (>= 5ms)
- No sweeps — static oscillators with envelope shaping

This creates an ear-training shortcut: *if it sweeps or sounds tense, it's an action; if it
sounds like a gentle chord, it's an agent.*

---

## Education Overlay

A first-time and on-demand tutorial panel that teaches the audio language in context.

### Trigger conditions
1. **Auto-show once**: when the user first enables sounds (localStorage key `boss_audio_tutorial_seen`)
2. **Manual access**: "Audio Guide" link in the sound settings panel

### Panel structure
The overlay is a non-blocking drawer (slides in from right, doesn't dim the page) with:

```
  OpenDispatch Audio Guide
  ──────────────────────

  Actions                          [play each]
  ● Task started        [▶]
  ● Task in review      [▶]
  ● Task completed      [▶]
  ● Agent spawned       [▶]
  ● PR shipped          [▶]

  Agents                           [play each]
  ● Each active agent   [▶ name]

  Alerts                           [play each]
  ● Agent blocked       [▶]
  ● @mention received   [▶]

  Messages                         [play each]
  ● Message sent        [▶]
  ● Two agents talking  [▶]  ← plays collaboration chord

  ─ Each [▶] button plays the sound in isolation.
  ─ The Actions section plays action-cue-only (no agent voice appended).
  ─ The Agents section uses real agent names from your space.
```

### Implementation plan
- Vue component: `AudioGuidePanel.vue` — slide-in drawer
- Triggered from settings (existing) via a "Open Audio Guide" button
- Each row: label + `<button @click="playSample(eventType)">` that calls the relevant function
- On first-enable: `watch(soundEnabled, (v) => { if (v && !tutorialSeen) showGuide = true })`

---

## Implementation Phases

### Phase 1 — Structural separation (code change)
Audit every `play*` function and verify it follows the distinguishability rules above. Any
action cue that currently uses a static pentatonic tone should be converted to a sweep or
given a dissonant interval.

### Phase 2 — Grammar wiring (code change)
Where appropriate, append the sender's identity voice after the action cue with the 80-150ms
gap. Start with: agent spawn (already done in principle), @mention, agent→agent message.

### Phase 3 — Education overlay (code change)
Implement `AudioGuidePanel.vue` and the first-time auto-show logic.

### Phase 4 — Review and tuning
Audio-sme listens to the full system and tunes volumes, gaps, and cue shapes.

---

## Open Questions for Review

1. **Gap duration:** 80-150ms between action and identity feels right on paper but needs
   listening tests. Should the gap scale with urgency (shorter for urgent)?

2. **Recipient voice in message events:** Adding the recipient's voice after the sender's
   in a message creates a nice "call-and-response" but fires on every message. Is this
   too frequent for social category? Consider making it opt-in or limiting to @mentions.

3. **Action cue volume relative to identity voice:** The action cue should be ~1.5x the
   identity voice volume so the "what" is always audible even in busy fleet states.

4. **Education panel auto-show timing:** Show immediately on sound enable, or after 3
   seconds once the user has seen the dashboard? Too early might be overwhelming.

5. **Theme coherence:** Do all 4 sound themes need unique action cues, or is it acceptable
   for action cues to be theme-invariant while only identity voices shift with theme?
   My recommendation: action cues are theme-invariant (structural/textural); identity voices
   shift with theme. This actually reinforces the distinguishability.

---

## Critique Requested

Please review with attention to:
- Is the `[ACTION_CUE] + [gap] + [SENDER_VOICE]` grammar intuitive or too abstract?
- Are the distinguishability rules (sweeps/noise/dissonance for actions; pentatonic for identity) achievable within the current synth architecture?
- Is the education overlay design adequate for teaching the grammar, or does it need an interactive demo mode?
- Any changes to the implementation phase ordering?
