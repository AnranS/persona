package persona.feature.settings

import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.TextView
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import com.google.zxing.BarcodeFormat
import com.google.zxing.client.android.Intents
import com.journeyapps.barcodescanner.CaptureActivity
import com.journeyapps.barcodescanner.CaptureManager
import com.journeyapps.barcodescanner.DecoratedBarcodeView
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Keeps ZXing capture behavior while providing pairing-specific instructions and actions. */
class PairingScanActivity : CaptureActivity() {
    private val photoEnabled get() = !intent.getBooleanExtra(DisablePhoto, false)
    private val photoContract = ActivityResultContracts.PickVisualMedia()
    private val photoScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    // The app's language, as in every other activity (docs/android/android.md "Internationalization").
    override fun attachBaseContext(base: android.content.Context) = super.attachBaseContext(persona.core.designsystem.AppLanguages.wrap(base))

    override fun initializeContent(): DecoratedBarcodeView {
        setContentView(R.layout.pairing_scan)
        return findViewById(R.id.zxing_barcode_scanner)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = findViewById<View>(R.id.pairing_scan_root)
        root.setOnApplyWindowInsetsListener { view, insets ->
            val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars())
            view.setPadding(bars.left, bars.top, bars.right, bars.bottom)
            insets
        }
        root.requestApplyInsets()
        findViewById<View>(R.id.pairing_scan_close).setOnClickListener { finish() }
        findViewById<Button>(R.id.pairing_scan_photo).apply {
            visibility = if (photoEnabled) View.VISIBLE else View.GONE
            setOnClickListener {
                val request = PickVisualMediaRequest.Builder()
                    .setMediaType(ActivityResultContracts.PickVisualMedia.ImageOnly).build()
                startActivityForResult(photoContract.createIntent(this@PairingScanActivity, request), PhotoRequest)
            }
        }
        findViewById<View>(R.id.pairing_scan_manual).setOnClickListener {
            setResult(RESULT_CANCELED, Intent().putExtra(ManualInput, true))
            finish()
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == PhotoRequest) {
            photoContract.parseResult(resultCode, data)?.let(::readPhoto)
            return
        }
        super.onActivityResult(requestCode, resultCode, data)
    }

    override fun onDestroy() {
        photoScope.cancel()
        super.onDestroy()
    }

    /** Camera denial leaves pairing's photo action usable; browser sign-in keeps the library's original denial result. */
    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        if (photoEnabled && requestCode == CaptureManager.getCameraPermissionReqCode() &&
            grantResults.firstOrNull() != PackageManager.PERMISSION_GRANTED) return
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
    }

    private fun readPhoto(uri: Uri) {
        val button = findViewById<Button>(R.id.pairing_scan_photo)
        findViewById<TextView>(R.id.pairing_scan_photo_error).visibility = View.GONE
        button.isEnabled = false
        button.setText(R.string.settings_pairing_scan_photo_reading)
        photoScope.launch {
            val outcome = try {
                withContext(Dispatchers.IO) { readPairingPhoto(contentResolver, uri) }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                PairingPhotoResult.Failure(PairingPhotoError.Unreadable)
            }
            when (outcome) {
                is PairingPhotoResult.Code -> {
                    val result = Intent(Intents.Scan.ACTION)
                        .addFlags(Intent.FLAG_ACTIVITY_CLEAR_WHEN_TASK_RESET)
                        .putExtra(Intents.Scan.RESULT, outcome.payload)
                        .putExtra(Intents.Scan.RESULT_FORMAT, BarcodeFormat.QR_CODE.name)
                    setResult(RESULT_OK, result)
                    finish()
                }
                is PairingPhotoResult.Failure -> {
                    val copy = when (outcome.error) {
                        PairingPhotoError.Unreadable -> R.string.settings_pairing_scan_photo_unreadable
                        PairingPhotoError.NoQR -> R.string.settings_pairing_scan_photo_none
                        PairingPhotoError.MultipleQR -> R.string.settings_pairing_scan_photo_multiple
                    }
                    findViewById<TextView>(R.id.pairing_scan_photo_error).apply {
                        setText(copy)
                        visibility = View.VISIBLE
                    }
                    button.isEnabled = true
                    button.setText(R.string.settings_pairing_scan_photo)
                }
            }
        }
    }

    companion object {
        const val ManualInput = "persona.feature.settings.manual_input"
        const val DisablePhoto = "persona.feature.settings.disable_photo"
        private const val PhotoRequest = 11057
    }
}
