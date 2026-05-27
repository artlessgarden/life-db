package com.xiang.lifedb

import android.content.Context
import android.content.Intent
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.graphics.BitmapFactory
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Bundle
import android.provider.OpenableColumns
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import java.io.IOException
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.UUID
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONObject

private const val SERVICE_TYPE = "_life-db._tcp."
private const val PREFS_NAME = "life_db_settings"
private const val DEFAULT_SYNC_INTERVAL_MS = 30_000L

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableStrongDisplay()

        setContent {
            MaterialTheme {
                LifeDbApp()
            }
        }
    }

    private fun enableStrongDisplay() {
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O_MR1) {
            setShowWhenLocked(true)
            setTurnScreenOn(true)
        } else {
            @Suppress("DEPRECATION")
            window.addFlags(
                WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or
                    WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON,
            )
        }
    }
}

data class Entry(
    val id: String,
    val content: String,
    val createdAt: Long,
    val updatedAt: Long,
    val deletedAt: Long?,
    val version: Long,
    val sourceDeviceId: String,
    val pendingAction: String? = null,
    val pendingBaseVersion: Long = 0,
)

data class PendingChange(
    val action: String,
    val baseVersion: Long,
    val entry: Entry,
)

data class SyncResponse(
    val entries: List<Entry>,
    val conflicts: List<String>,
)

data class AppColors(
    val background: Color,
    val panel: Color,
    val text: Color,
    val hint: Color,
    val divider: Color,
    val status: Color,
)

@Composable
private fun LifeDbApp() {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val store = remember { LifeDbStore(context) }
    val client = remember { LifeDbClient() }

    var entries by remember { mutableStateOf(store.listVisibleEntries()) }
    var input by remember { mutableStateOf("") }
    var selected by remember { mutableStateOf<Entry?>(null) }
    var editing by remember { mutableStateOf<Entry?>(null) }
    var editText by remember { mutableStateOf("") }
    var status by remember { mutableStateOf("发现电脑中") }
    var baseUrl by remember { mutableStateOf(store.getServerBaseUrl()) }
    var backgroundUri by remember { mutableStateOf(store.getBackgroundUri()) }
    var backgroundOpacity by remember { mutableStateOf(store.getBackgroundOpacity()) }
    var isSyncing by remember { mutableStateOf(false) }

    val colors = appColors(backgroundOpacity)

    fun reloadLocal() {
        entries = store.listVisibleEntries()
    }

    fun syncNow() {
        scope.launch {
            val url = baseUrl
            if (url.isNullOrBlank() || isSyncing) {
                if (url.isNullOrBlank()) status = "未发现电脑"
                return@launch
            }

            isSyncing = true
            status = "同步中"
            try {
                val response = client.sync(
                    baseUrl = url,
                    deviceId = store.deviceId,
                    changes = store.pendingChanges(),
                )
                store.replaceFromServer(response.entries)
                reloadLocal()
                status = if (response.conflicts.isEmpty()) {
                    "已同步 ${formatClockTime(System.currentTimeMillis())}"
                } else {
                    "已同步，有冲突 ${response.conflicts.size}"
                }
            } catch (_: IOException) {
                status = "离线"
            } catch (_: Exception) {
                status = "同步失败"
            } finally {
                isSyncing = false
            }
        }
    }

    fun createLocalEntry(text: String) {
        val trimmed = text.trim()
        if (trimmed.isBlank()) return
        store.createLocal(trimmed)
        input = ""
        reloadLocal()
        status = "已本地保存"
        syncNow()
    }

    fun updateLocalEntry(entry: Entry, newContent: String) {
        val trimmed = newContent.trim()
        if (trimmed.isBlank()) return
        store.updateLocal(entry, trimmed)
        editing = null
        reloadLocal()
        status = "已本地更新"
        syncNow()
    }

    fun deleteLocalEntry(entry: Entry) {
        store.deleteLocal(entry)
        selected = null
        reloadLocal()
        status = "已本地删除"
        syncNow()
    }

    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri != null) {
            runCatching {
                context.contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION,
                )
            }
            store.setBackgroundUri(uri.toString())
            backgroundUri = uri.toString()
        }
    }

    DisposableEffect(Unit) {
        val discovery = LifeDbDiscovery(context) { discoveredUrl ->
            baseUrl = discoveredUrl
            store.setServerBaseUrl(discoveredUrl)
            status = "发现电脑 $discoveredUrl"
            syncNow()
        }
        discovery.start()
        onDispose { discovery.stop() }
    }

    DisposableEffect(baseUrl) {
        val url = baseUrl
        if (url.isNullOrBlank()) {
            onDispose { }
        } else {
            val socket = client.openWebSocket(url) {
                syncNow()
            }
            onDispose { socket.close(1000, "closed") }
        }
    }

    LaunchedEffect(Unit) {
        reloadLocal()
        syncNow()
        while (true) {
            delay(DEFAULT_SYNC_INTERVAL_MS)
            syncNow()
        }
    }

    Box(modifier = Modifier.fillMaxSize()) {
        BackgroundLayer(uriString = backgroundUri)

        Surface(
            modifier = Modifier.fillMaxSize(),
            color = colors.panel,
        ) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 20.dp, vertical = 14.dp)
                    .imePadding(),
            ) {
                Header(
                    colors = colors,
                    status = status,
                    opacity = backgroundOpacity,
                    onOpacityChange = {
                        backgroundOpacity = it
                        store.setBackgroundOpacity(it)
                    },
                    onPickBackground = {
                        imagePicker.launch(arrayOf("image/*"))
                    },
                    onClearBackground = {
                        backgroundUri = null
                        store.setBackgroundUri(null)
                    },
                    onSync = ::syncNow,
                )

                Timeline(
                    entries = entries,
                    colors = colors,
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxWidth(),
                    onLongPress = { selected = it },
                )

                InputBar(
                    value = input,
                    colors = colors,
                    onValueChange = { input = it },
                    onSubmit = { createLocalEntry(input) },
                )
            }
        }
    }

    if (selected != null) {
        EntryMenuDialog(
            entry = selected!!,
            onDismiss = { selected = null },
            onEdit = {
                editText = selected!!.content
                editing = selected
                selected = null
            },
            onDelete = { deleteLocalEntry(selected!!) },
        )
    }

    if (editing != null) {
        EditEntryDialog(
            value = editText,
            onValueChange = { editText = it },
            onDismiss = { editing = null },
            onSave = { updateLocalEntry(editing!!, editText) },
        )
    }
}

@Composable
private fun appColors(opacity: Float): AppColors {
    return if (isSystemInDarkTheme()) {
        AppColors(
            background = Color(0xFF101010),
            panel = Color(0xFF101010).copy(alpha = opacity),
            text = Color(0xFFEDEDED),
            hint = Color(0xFF8F8F8F),
            divider = Color(0xFF4A4A4A),
            status = Color(0xFF9A9A9A),
        )
    } else {
        AppColors(
            background = Color(0xFFFDFDFB),
            panel = Color(0xFFFDFDFB).copy(alpha = opacity),
            text = Color(0xFF111111),
            hint = Color(0xFF777777),
            divider = Color(0x33000000),
            status = Color(0xFF777777),
        )
    }
}

@Composable
private fun BackgroundLayer(uriString: String?) {
    if (uriString.isNullOrBlank()) return
    val context = LocalContext.current
    var bitmap by remember(uriString) { mutableStateOf<android.graphics.Bitmap?>(null) }

    LaunchedEffect(uriString) {
        bitmap = withContext(Dispatchers.IO) {
            runCatching {
                context.contentResolver.openInputStream(Uri.parse(uriString)).use { input ->
                    BitmapFactory.decodeStream(input)
                }
            }.getOrNull()
        }
    }

    val current = bitmap
    if (current != null) {
        Image(
            bitmap = current.asImageBitmap(),
            contentDescription = null,
            modifier = Modifier.fillMaxSize(),
            contentScale = ContentScale.Crop,
        )
    }
}

@Composable
private fun Header(
    colors: AppColors,
    status: String,
    opacity: Float,
    onOpacityChange: (Float) -> Unit,
    onPickBackground: () -> Unit,
    onClearBackground: () -> Unit,
    onSync: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 4.dp, bottom = 12.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = formatHeaderTime(System.currentTimeMillis()),
            color = colors.text,
            fontSize = 22.sp,
            fontWeight = FontWeight.Medium,
            textAlign = TextAlign.Center,
        )
        Text(
            text = status,
            color = colors.status,
            fontSize = 13.sp,
            modifier = Modifier.padding(top = 3.dp),
        )
        Row(
            modifier = Modifier.padding(top = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = onSync) { Text("同步") }
            TextButton(onClick = onPickBackground) { Text("背景") }
            TextButton(onClick = onClearBackground) { Text("清空") }
        }
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(text = "透明", color = colors.status, fontSize = 12.sp)
            Slider(
                value = opacity,
                onValueChange = onOpacityChange,
                valueRange = 0.25f..1f,
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Timeline(
    entries: List<Entry>,
    colors: AppColors,
    modifier: Modifier = Modifier,
    onLongPress: (Entry) -> Unit,
) {
    val listState = rememberLazyListState()

    LaunchedEffect(entries.size) {
        if (entries.isNotEmpty()) {
            listState.animateScrollToItem(entries.lastIndex)
        }
    }

    LazyColumn(
        modifier = modifier,
        state = listState,
        contentPadding = PaddingValues(horizontal = 4.dp, vertical = 18.dp),
        verticalArrangement = Arrangement.Bottom,
    ) {
        itemsIndexed(entries) { index, entry ->
            if (index > 0) {
                Spacer(
                    modifier = Modifier.height(
                        gapHeightBetween(entries[index - 1], entry),
                    ),
                )
            }
            EntryRow(
                entry = entry,
                colors = colors,
                onLongPress = onLongPress,
            )
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun EntryRow(
    entry: Entry,
    colors: AppColors,
    onLongPress: (Entry) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(
                onClick = {},
                onLongClick = { onLongPress(entry) },
            ),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = formatClockTime(entry.createdAt),
            modifier = Modifier.width(64.dp),
            color = colors.text,
            fontSize = 21.sp,
            fontWeight = FontWeight.Bold,
        )
        Text(
            text = entry.content,
            color = colors.text,
            fontSize = 21.sp,
            lineHeight = 28.sp,
            maxLines = 4,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable
private fun InputBar(
    value: String,
    colors: AppColors,
    onValueChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(1.dp)
                .background(colors.divider),
        )
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier
                .fillMaxWidth()
                .padding(top = 13.dp, bottom = 8.dp),
            singleLine = true,
            textStyle = TextStyle(
                color = colors.text,
                fontSize = 21.sp,
                lineHeight = 28.sp,
            ),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(onDone = { onSubmit() }),
            decorationBox = { innerTextField ->
                Box(
                    modifier = Modifier.fillMaxWidth(),
                    contentAlignment = Alignment.CenterStart,
                ) {
                    if (value.isBlank()) {
                        Text(
                            text = "深呼吸，说点什么...",
                            color = colors.hint,
                            fontSize = 21.sp,
                        )
                    }
                    innerTextField()
                }
            },
        )
    }
}

@Composable
private fun EntryMenuDialog(
    entry: Entry,
    onDismiss: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(formatClockTime(entry.createdAt)) },
        text = { Text(entry.content, maxLines = 4, overflow = TextOverflow.Ellipsis) },
        confirmButton = { TextButton(onClick = onEdit) { Text("编辑") } },
        dismissButton = {
            Row {
                TextButton(onClick = onDelete) { Text("删除", color = Color(0xFFB00020)) }
                TextButton(onClick = onDismiss) { Text("取消") }
            }
        },
    )
}

@Composable
private fun EditEntryDialog(
    value: String,
    onValueChange: (String) -> Unit,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("编辑") },
        text = {
            TextField(
                value = value,
                onValueChange = onValueChange,
                modifier = Modifier.fillMaxWidth(),
                minLines = 3,
            )
        },
        confirmButton = { Button(onClick = onSave) { Text("保存") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } },
    )
}

class LifeDbStore(context: Context) : SQLiteOpenHelper(context, "life-db-local.db", null, 1) {
    private val appContext = context.applicationContext
    private val prefs = appContext.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    val deviceId: String = prefs.getString("device_id", null) ?: run {
        val id = "android-" + UUID.randomUUID().toString().take(8)
        prefs.edit().putString("device_id", id).apply()
        id
    }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL(
            """
            CREATE TABLE entries (
                id TEXT PRIMARY KEY,
                content TEXT NOT NULL,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL,
                deleted_at INTEGER,
                version INTEGER NOT NULL,
                source_device_id TEXT NOT NULL,
                pending_action TEXT,
                pending_base_version INTEGER NOT NULL DEFAULT 0
            )
            """.trimIndent(),
        )
        db.execSQL("CREATE INDEX idx_entries_created_at ON entries(created_at)")
        db.execSQL("CREATE INDEX idx_entries_updated_at ON entries(updated_at)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    fun getServerBaseUrl(): String? = prefs.getString("server_base_url", null)

    fun setServerBaseUrl(value: String) {
        prefs.edit().putString("server_base_url", value).apply()
    }

    fun getBackgroundUri(): String? = prefs.getString("background_uri", null)

    fun setBackgroundUri(value: String?) {
        prefs.edit().apply {
            if (value == null) remove("background_uri") else putString("background_uri", value)
        }.apply()
    }

    fun getBackgroundOpacity(): Float = prefs.getFloat("background_opacity", 0.88f)

    fun setBackgroundOpacity(value: Float) {
        prefs.edit().putFloat("background_opacity", value).apply()
    }

    fun listVisibleEntries(): List<Entry> {
        val db = readableDatabase
        val cursor = db.rawQuery(
            "SELECT id, content, created_at, updated_at, deleted_at, version, source_device_id, pending_action, pending_base_version FROM entries WHERE deleted_at IS NULL ORDER BY created_at ASC",
            emptyArray(),
        )
        return cursor.use {
            buildList {
                while (it.moveToNext()) add(cursorToEntry(it))
            }
        }
    }

    fun createLocal(content: String) {
        val now = System.currentTimeMillis()
        writableDatabase.execSQL(
            "INSERT INTO entries (id, content, created_at, updated_at, deleted_at, version, source_device_id, pending_action, pending_base_version) VALUES (?, ?, ?, ?, NULL, 0, ?, 'create', 0)",
            arrayOf(UUID.randomUUID().toString(), content, now, now, deviceId),
        )
    }

    fun updateLocal(entry: Entry, content: String) {
        val pendingAction = if (entry.pendingAction == "create") "create" else "update"
        val baseVersion = if (entry.pendingAction == "create") 0 else entry.version
        writableDatabase.execSQL(
            "UPDATE entries SET content = ?, updated_at = ?, pending_action = ?, pending_base_version = ? WHERE id = ?",
            arrayOf(content, System.currentTimeMillis(), pendingAction, baseVersion, entry.id),
        )
    }

    fun deleteLocal(entry: Entry) {
        if (entry.pendingAction == "create") {
            writableDatabase.execSQL("DELETE FROM entries WHERE id = ?", arrayOf(entry.id))
            return
        }
        writableDatabase.execSQL(
            "UPDATE entries SET deleted_at = ?, updated_at = ?, pending_action = 'delete', pending_base_version = ? WHERE id = ?",
            arrayOf(System.currentTimeMillis(), System.currentTimeMillis(), entry.version, entry.id),
        )
    }

    fun pendingChanges(): List<PendingChange> {
        val db = readableDatabase
        val cursor = db.rawQuery(
            "SELECT id, content, created_at, updated_at, deleted_at, version, source_device_id, pending_action, pending_base_version FROM entries WHERE pending_action IS NOT NULL ORDER BY updated_at ASC",
            emptyArray(),
        )
        return cursor.use {
            buildList {
                while (it.moveToNext()) {
                    val entry = cursorToEntry(it)
                    val action = entry.pendingAction ?: continue
                    add(PendingChange(action, entry.pendingBaseVersion, entry))
                }
            }
        }
    }

    fun replaceFromServer(entries: List<Entry>) {
        val db = writableDatabase
        db.beginTransaction()
        try {
            db.execSQL("DELETE FROM entries")
            for (entry in entries) {
                db.execSQL(
                    "INSERT INTO entries (id, content, created_at, updated_at, deleted_at, version, source_device_id, pending_action, pending_base_version) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, 0)",
                    arrayOf(
                        entry.id,
                        entry.content,
                        entry.createdAt,
                        entry.updatedAt,
                        entry.deletedAt,
                        entry.version,
                        entry.sourceDeviceId,
                    ),
                )
            }
            db.setTransactionSuccessful()
        } finally {
            db.endTransaction()
        }
    }

    private fun cursorToEntry(cursor: android.database.Cursor): Entry {
        val deleted = if (cursor.isNull(4)) null else cursor.getLong(4)
        return Entry(
            id = cursor.getString(0),
            content = cursor.getString(1),
            createdAt = cursor.getLong(2),
            updatedAt = cursor.getLong(3),
            deletedAt = deleted,
            version = cursor.getLong(5),
            sourceDeviceId = cursor.getString(6),
            pendingAction = if (cursor.isNull(7)) null else cursor.getString(7),
            pendingBaseVersion = cursor.getLong(8),
        )
    }
}

class LifeDbClient {
    private val http = OkHttpClient()
    private val jsonMediaType = "application/json; charset=utf-8".toMediaType()

    suspend fun sync(baseUrl: String, deviceId: String, changes: List<PendingChange>): SyncResponse {
        return withContext(Dispatchers.IO) {
            val body = JSONObject()
                .put("device_id", deviceId)
                .put("changes", JSONArray().apply {
                    for (change in changes) {
                        put(
                            JSONObject()
                                .put("action", change.action)
                                .put("base_version", change.baseVersion)
                                .put("entry", entryToJson(change.entry)),
                        )
                    }
                })

            val request = Request.Builder()
                .url("${baseUrl.trimEnd('/')}/api/sync")
                .post(body.toString().toRequestBody(jsonMediaType))
                .build()

            http.newCall(request).execute().use { response ->
                if (!response.isSuccessful) throw IOException("sync failed: ${response.code}")
                val text = response.body?.string().orEmpty()
                parseSyncResponse(text)
            }
        }
    }

    fun openWebSocket(baseUrl: String, onChanged: () -> Unit): WebSocket {
        val wsUrl = baseUrl.trimEnd('/').replaceFirst("http://", "ws://").replaceFirst("https://", "wss://") + "/ws"
        val request = Request.Builder().url(wsUrl).build()
        return http.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onMessage(webSocket: WebSocket, text: String) {
                    onChanged()
                }
            },
        )
    }

    private fun entryToJson(entry: Entry): JSONObject {
        return JSONObject()
            .put("id", entry.id)
            .put("content", entry.content)
            .put("created_at", entry.createdAt)
            .put("updated_at", entry.updatedAt)
            .put("deleted_at", entry.deletedAt)
            .put("version", entry.version)
            .put("source_device_id", entry.sourceDeviceId)
    }

    private fun parseSyncResponse(text: String): SyncResponse {
        val json = JSONObject(text)
        val entriesArray = json.getJSONArray("entries")
        val entries = buildList {
            for (index in 0 until entriesArray.length()) {
                val item = entriesArray.getJSONObject(index)
                add(jsonToEntry(item))
            }
        }
        val conflictsArray = json.optJSONArray("conflicts") ?: JSONArray()
        val conflicts = buildList {
            for (index in 0 until conflictsArray.length()) add(conflictsArray.getString(index))
        }
        return SyncResponse(entries, conflicts)
    }

    private fun jsonToEntry(item: JSONObject): Entry {
        val deletedAt = if (item.isNull("deleted_at")) null else item.getLong("deleted_at")
        return Entry(
            id = item.getString("id"),
            content = item.getString("content"),
            createdAt = item.getLong("created_at"),
            updatedAt = item.getLong("updated_at"),
            deletedAt = deletedAt,
            version = item.getLong("version"),
            sourceDeviceId = item.optString("source_device_id", "server"),
        )
    }
}

class LifeDbDiscovery(
    private val context: Context,
    private val onFound: (String) -> Unit,
) {
    private val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager
    private var discoveryListener: NsdManager.DiscoveryListener? = null
    private var multicastLock: WifiManager.MulticastLock? = null

    fun start() {
        acquireMulticastLock()
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) = Unit
            override fun onDiscoveryStopped(serviceType: String) = Unit
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) = Unit
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) = Unit

            override fun onServiceLost(serviceInfo: NsdServiceInfo) = Unit

            override fun onServiceFound(serviceInfo: NsdServiceInfo) {
                if (serviceInfo.serviceType != SERVICE_TYPE) return
                resolve(serviceInfo)
            }
        }
        discoveryListener = listener
        runCatching {
            nsdManager.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener)
        }
    }

    fun stop() {
        discoveryListener?.let { listener ->
            runCatching { nsdManager.stopServiceDiscovery(listener) }
        }
        discoveryListener = null
        multicastLock?.let { lock -> if (lock.isHeld) lock.release() }
        multicastLock = null
    }

    private fun resolve(serviceInfo: NsdServiceInfo) {
        runCatching {
            nsdManager.resolveService(
                serviceInfo,
                object : NsdManager.ResolveListener {
                    override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) = Unit

                    override fun onServiceResolved(resolved: NsdServiceInfo) {
                        val address = resolved.host?.hostAddress ?: return
                        val host = if (address.contains(':')) "[$address]" else address
                        val url = "http://$host:${resolved.port}"
                        onFound(url)
                    }
                },
            )
        }
    }

    private fun acquireMulticastLock() {
        val wifiManager = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
            ?: return
        multicastLock = wifiManager.createMulticastLock("life-db-mdns").apply {
            setReferenceCounted(true)
            acquire()
        }
    }
}

private fun gapHeightBetween(previous: Entry, current: Entry): Dp {
    val minutes = ((current.createdAt - previous.createdAt) / 60_000L).coerceAtLeast(0L)
    val level = (minutes / 30L).coerceIn(0L, 5L).toInt()
    return (18 + level * 24).dp
}

private fun formatHeaderTime(timestampMillis: Long): String {
    val date = Instant.ofEpochMilli(timestampMillis).atZone(ZoneId.systemDefault()).toLocalDate()
    val weekText = when (date.dayOfWeek.value) {
        1 -> "一"
        2 -> "二"
        3 -> "三"
        4 -> "四"
        5 -> "五"
        6 -> "六"
        else -> "日"
    }
    return "${date.year}年${date.monthValue}月${date.dayOfMonth}日  周$weekText"
}

private fun formatClockTime(timestampMillis: Long): String {
    val time = Instant.ofEpochMilli(timestampMillis).atZone(ZoneId.systemDefault()).toLocalTime()
    return time.format(DateTimeFormatter.ofPattern("H:mm"))
}
