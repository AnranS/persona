package persona.platform.shortcuts

import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.graphics.drawable.Icon
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import persona.core.domain.fleet.FleetSummary

/**
 * The Quick Settings tile (iOS "Persona status"): "2 need you" / "3 working" at a glance, lit while something needs you or
 * works. A tap opens the only agent when there is one, otherwise Agents.
 */
class PersonaTileService : TileService() {
    private val renderScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    /** A tap must still navigate if closing the shade destroys this service while restoration is pending. */
    private val clickScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @Volatile private var destroyed = false

    override fun onCreate() {
        super.onCreate()
        destroyed = false
    }

    override fun onStartListening() {
        super.onStartListening()
        val dependencies = SurfacesDependencies.of(this)
        renderScope.launch(start = CoroutineStart.UNDISPATCHED) {
            dependencies.fleet().settled()
            render()
        }
    }

    override fun onClick() {
        super.onClick()
        val dependencies = SurfacesDependencies.of(this)
        clickScope.launch(start = CoroutineStart.UNDISPATCHED) {
            val feed = dependencies.fleet()
            feed.settled()
            val intent = SurfaceLinks.fleet(applicationContext, feed.paired.value, feed.state.value) { dependencies.sessions().assistant(it) }
            open(intent)
        }
    }

    @Synchronized override fun onDestroy() {
        destroyed = true
        renderScope.cancel()
        super.onDestroy()
    }

    @Synchronized private fun open(intent: Intent) {
        if (destroyed) {
            applicationContext.startActivity(intent)
            return
        }
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(PendingIntent.getActivity(applicationContext, REQUEST, intent,
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        } else {
            @Suppress("DEPRECATION", "StartActivityAndCollapseDeprecated")
            startActivityAndCollapse(intent)
        }
    }

    private fun render() {
        val tile = qsTile ?: return
        val feed = SurfacesDependencies.of(this).fleet()
        val paired = feed.paired.value
        val fleet = feed.state.value
        val subtitle = ShortcutText.tileSubtitle(resources, paired, fleet)
        tile.label = getString(R.string.shortcuts_tile_label)
        tile.subtitle = subtitle
        tile.icon = Icon.createWithResource(this, persona.core.designsystem.R.drawable.ic_stat)
        tile.state = when {
            !paired -> Tile.STATE_UNAVAILABLE
            FleetSummary.phase(fleet) == FleetSummary.NEEDS_YOU || FleetSummary.phase(fleet) == FleetSummary.WORKING -> Tile.STATE_ACTIVE
            else -> Tile.STATE_INACTIVE
        }
        tile.contentDescription = getString(R.string.shortcuts_tile_description, subtitle)
        tile.updateTile()
    }

    companion object {
        private const val REQUEST = 0x7400

        /** Asks the system to redraw the tile: it calls onStartListening while the shade shows it. */
        fun refresh(context: Context) {
            runCatching { requestListeningState(context, ComponentName(context, PersonaTileService::class.java)) }
        }
    }
}
