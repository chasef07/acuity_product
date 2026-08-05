# Telnyx skills

These are exact copies of the relevant official skills from
[`team-telnyx/ai`](https://github.com/team-telnyx/ai) at commit
`109e4cda6f257038fc504abbd5e646597d781d2e`:

- `telnyx-voice-go`: Call Control and webhooks.
- `telnyx-voice-media-go`: playback, speech, and recording.
- `telnyx-webrtc-client-js`: browser softphone and media events.
- `telnyx-webrtc-go`: server-side softphone credentials and tokens.
- `telnyx-sip-go`: connections, credentials, and voice profiles.
- `telnyx-messaging-go`: SMS, MMS, and messaging webhooks.
- `telnyx-numbers-config-go`: number assignments and voice settings.

The skills are provider references, not Acuity architecture. The governing
GitHub issue, `docs/agents/domain.md`, current application boundaries, and tests
take precedence. Generated Go SDK examples do not authorize replacing the
existing provider adapter or adding the Telnyx Go SDK. Verify time-sensitive
Telnyx behavior and live account settings before making provider changes.

Never put Telnyx credentials in source, documentation, logs, or commands that
will be committed.

The copied files are MIT licensed. See `LICENSE.team-telnyx-ai`.

## Update

Review upstream changes, then refresh all seven copies and their hashes with:

```sh
npx skills add team-telnyx/ai \
  --skill telnyx-voice-go telnyx-voice-media-go telnyx-webrtc-client-js \
  telnyx-webrtc-go telnyx-sip-go telnyx-messaging-go \
  telnyx-numbers-config-go \
  --agent codex --copy --yes
```

Record the new upstream commit above and review the complete diff before
committing it.
