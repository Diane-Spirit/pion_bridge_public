package com.diane.pionbridge

import android.content.Context
import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.diane.pionbridge.ui.theme.PionBridgeTheme
import androidx.core.content.edit

class MainActivity : ComponentActivity() {

    private val prefs by lazy {
        getSharedPreferences("service_prefs", Context.MODE_PRIVATE)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // Create a mutable state that holds the current service running status.
        // We read the persisted value from SharedPreferences.
        setContent {
            val serviceRunningState = remember {
                mutableStateOf(prefs.getBoolean("serviceRunning", false))
            }

            PionBridgeTheme {
                Scaffold(modifier = Modifier.fillMaxSize()) { innerPadding ->
                    ServiceControlUI(
                        modifier = Modifier.padding(innerPadding),
                        isServiceRunning = serviceRunningState.value,
                        onStartService = {
                            startBridgeService()
                            serviceRunningState.value = true
                        },
                        onStopService = {
                            stopBridgeService()
                            serviceRunningState.value = false
                        }
                    )
                }
            }
        }
    }

    private fun startBridgeService() {
        val intent = Intent(this, BridgeService::class.java)
        startForegroundService(intent)
        // Persist the service running state.
        prefs.edit { putBoolean("serviceRunning", true) }
    }

    private fun stopBridgeService() {
        val intent = Intent(this, BridgeService::class.java).apply {
            action = BridgeService.ACTION_STOP
        }
        // Use startService() to deliver the stop signal to the running service.
        startService(intent)
        // Clear the flag when the service stops.
        prefs.edit { putBoolean("serviceRunning", false) }
    }
}

@Composable
fun ServiceControlUI(
    modifier: Modifier = Modifier,
    isServiceRunning: Boolean,
    onStartService: () -> Unit,
    onStopService: () -> Unit
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center
    ) {
        Text(
            text = "PionBridge Service Controller",
            style = MaterialTheme.typography.headlineMedium
        )
        Spacer(modifier = Modifier.height(20.dp))

        Button(
            onClick = onStartService,
            enabled = !isServiceRunning // Disable Start button if service is already running
        ) {
            Text("Start Service")
        }
        Spacer(modifier = Modifier.height(10.dp))

        Button(
            onClick = onStopService,
            colors = ButtonDefaults.buttonColors(MaterialTheme.colorScheme.error),
            enabled = isServiceRunning // Disable Stop button if service is not running
        ) {
            Text("Stop Service")
        }
    }
}

@Preview(showBackground = true)
@Composable
fun PreviewServiceControlUI() {
    PionBridgeTheme {
        // For preview purposes, we assume the service is not running.
        ServiceControlUI(isServiceRunning = false, onStartService = {}, onStopService = {})
    }
}
