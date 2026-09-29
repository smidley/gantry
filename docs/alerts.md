# Reducing backup notification noise

If short backup interruptions produce **Container unhealthy** and
**Recovered** notifications, open **Alerts → Rules → Container unhealthy →
Edit** and set **Unhealthy for (seconds)** to a delay such as **300** (five
minutes), then **Save**. The rule summary shows the saved delay.

Gantry waits for a running container to remain unhealthy for that duration.
If it becomes healthy, stops, or disappears during the delay, the pending
alert closes without an unhealthy or recovery notification. A container
that stays running and unhealthy beyond the delay still alerts normally.
Gantry also checks the live container state before sending an immediate
health alert, so an unhealthy event for a stopped container stays quiet.

The delay accepts whole seconds from **0** to **3600**. **0** keeps immediate
notification; existing rule settings are preserved on upgrade. This is a
health delay, not a scheduled maintenance window: a container that remains
running and unhealthy during a long backup can still alert after the delay.

For exit notifications, edit **Container exited nonzero → Restart grace
period (seconds)**. That separate setting ignores an exit if the container
starts again within the configured time.

Use an active alert's **Silence** menu for a temporary mute, or turn off a
rule with its **Enabled** checkbox if you do not want that alert at all.
Silencing preserves alert history. If an alert fires while silenced and
recovers before any notification is sent, Gantry records the recovery
without sending a standalone recovery notification.
