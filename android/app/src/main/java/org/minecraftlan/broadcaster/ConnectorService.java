package org.minecraftlan.broadcaster;

import android.app.*;
import android.content.*;
import android.net.*;
import android.net.wifi.WifiManager;
import android.os.*;
import java.util.concurrent.*;
import org.minecraftlan.nativecore.mobile.Mobile;

public class ConnectorService extends Service {
 public static volatile String status = "Ready to connect";
 private final ExecutorService worker = Executors.newSingleThreadExecutor();
 private WifiManager.MulticastLock multicast;
 private volatile boolean starting;
 private final Handler handler = new Handler(Looper.getMainLooper());
 private final Runnable monitor = new Runnable() {
  public void run() {
   if (!starting && !Mobile.isRunning()) { stopSelf(); return; }
   handler.postDelayed(this, 1500);
  }
 };
 @Override public void onCreate() {
  super.onCreate();
  NotificationManager nm = getSystemService(NotificationManager.class);
  nm.createNotificationChannel(new NotificationChannel("lan", "LAN connector", NotificationManager.IMPORTANCE_LOW));
 }
 @Override public int onStartCommand(Intent intent, int flags, int startId) {
  if (intent != null && "STOP".equals(intent.getAction())) { stopSelf(); return START_NOT_STICKY; }
  if (starting || Mobile.isRunning()) return START_NOT_STICKY;
  starting = true;
  String target = intent == null ? "198.51.100.10:19132" : intent.getStringExtra("target");
  String javaTarget = intent == null ? "198.51.100.10:25565" : intent.getStringExtra("javaTarget");
  boolean bedrock = intent == null || intent.getBooleanExtra("bedrock",true);
  boolean java = intent == null || intent.getBooleanExtra("java",true);
  PendingIntent open = PendingIntent.getActivity(this,0,new Intent(this,MainActivity.class),PendingIntent.FLAG_IMMUTABLE);
  PendingIntent stop = PendingIntent.getService(this,1,new Intent(this,ConnectorService.class).setAction("STOP"),PendingIntent.FLAG_IMMUTABLE);
  Notification n = new Notification.Builder(this,"lan")
    .setSmallIcon(android.R.drawable.stat_sys_upload).setContentTitle("Minecraft LAN Broadcaster")
    .setContentText("Advertising your server on Wi-Fi").setContentIntent(open).setOngoing(true)
    .addAction(new Notification.Action.Builder(null,"Stop",stop).build()).build();
  startForeground(1,n);
  status = "Starting LAN broadcast…";
  worker.execute(() -> {
   try {
    ConnectivityManager cm = getSystemService(ConnectivityManager.class);
    String iface = null;
    for (Network network : cm.getAllNetworks()) {
     NetworkCapabilities caps = cm.getNetworkCapabilities(network);
     LinkProperties lp = cm.getLinkProperties(network);
     if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) && lp != null) {
      iface = lp.getInterfaceName(); break;
     }
    }
    if (iface == null) throw new Exception("Connect this phone to the console’s Wi-Fi first.");
    WifiManager wifi = (WifiManager)getApplicationContext().getSystemService(WIFI_SERVICE);
    multicast = wifi.createMulticastLock("minecraft-lan-broadcaster-discovery");
    multicast.setReferenceCounted(false); multicast.acquire();
    Mobile.setLogPath(new java.io.File(getFilesDir(),"connector.log").getAbsolutePath());
    Mobile.startSelected(target,javaTarget,iface,bedrock,java);
    status = bedrock && java ? "Broadcasting Bedrock + Java" : (bedrock ? "Broadcasting Bedrock" : "Broadcasting Java");
   } catch (Exception e) {
    status = "Could not start: " + e.getMessage();
    handler.post(this::stopSelf);
   } finally { starting = false; }
  });
  handler.postDelayed(monitor,1500);
  return START_NOT_STICKY;
 }
 @Override public void onDestroy() {
  handler.removeCallbacksAndMessages(null);
  worker.execute(() -> {
   try { Mobile.stop(); } catch (Exception ignored) {}
   if (multicast != null && multicast.isHeld()) multicast.release();
   if (!status.startsWith("Could not start:")) status = "Stopped";
  });
  worker.shutdown(); stopForeground(STOP_FOREGROUND_REMOVE); super.onDestroy();
 }
 @Override public IBinder onBind(Intent i) { return null; }
}
