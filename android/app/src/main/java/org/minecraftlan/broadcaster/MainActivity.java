package org.minecraftlan.broadcaster;

import android.Manifest;
import android.app.*;
import android.content.*;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.*;
import android.view.*;
import android.widget.*;
import org.minecraftlan.nativecore.mobile.Mobile;

public class MainActivity extends Activity {
 private final int ink=Color.rgb(230,242,238), muted=Color.rgb(154,177,169), green=Color.rgb(111,228,169);
 private EditText target, javaTarget;
 private Switch bedrockToggle, javaToggle;
 private TextView state, logs;
 private Button start, stop, publicChoice, localChoice;
 private final Handler handler=new Handler(Looper.getMainLooper());
 private int dp(int n){return (int)(n*getResources().getDisplayMetrics().density);}
 private TextView text(String s,int size,int color){TextView v=new TextView(this);v.setText(s);v.setTextSize(size);v.setTextColor(color);v.setPadding(0,dp(5),0,dp(5));return v;}
 private GradientDrawable background(int color){GradientDrawable d=new GradientDrawable();d.setColor(color);d.setCornerRadius(dp(15));return d;}
 private Button button(String label){Button b=new Button(this);b.setText(label);b.setAllCaps(false);b.setTextColor(ink);b.setBackgroundTintList(android.content.res.ColorStateList.valueOf(Color.rgb(39,65,54)));return b;}
 @Override public void onCreate(Bundle saved){
  super.onCreate(saved);
  getWindow().setStatusBarColor(Color.rgb(12,25,21));getWindow().setNavigationBarColor(Color.rgb(12,25,21));
  ScrollView scroll=new ScrollView(this);scroll.setFillViewport(true);scroll.setBackgroundColor(Color.rgb(12,25,21));
  LinearLayout content=new LinearLayout(this);content.setOrientation(LinearLayout.VERTICAL);content.setPadding(dp(24),dp(28),dp(24),dp(30));scroll.addView(content);setContentView(scroll);
  TextView brand=text("MINECRAFT  /  LAN BROADCASTER",12,green);brand.setLetterSpacing(.12f);content.addView(brand);
  content.addView(text("Make your server appear on the local network for Bedrock consoles and Java computers.",16,muted));
  LinearLayout card=new LinearLayout(this);card.setOrientation(LinearLayout.VERTICAL);card.setPadding(dp(18),dp(14),dp(18),dp(18));card.setBackground(background(Color.rgb(23,42,34)));
  LinearLayout.LayoutParams cp=new LinearLayout.LayoutParams(-1,-2);cp.setMargins(0,dp(18),0,dp(18));content.addView(card,cp);
  card.addView(text("EDITIONS & DESTINATIONS",11,green));
  bedrockToggle=new Switch(this);bedrockToggle.setText("Bedrock Edition · port 19132");bedrockToggle.setTextColor(ink);bedrockToggle.setTextSize(16);bedrockToggle.setMinHeight(dp(48));bedrockToggle.setChecked(getPreferences(0).getBoolean("bedrock",true));card.addView(bedrockToggle);
  target=new EditText(this);target.setSingleLine(true);target.setTextColor(ink);target.setTextSize(16);target.setInputType(android.text.InputType.TYPE_CLASS_TEXT|android.text.InputType.TYPE_TEXT_VARIATION_URI);
  target.setText(getPreferences(0).getString("target","198.51.100.10:19132"));card.addView(target);
  javaToggle=new Switch(this);javaToggle.setText("Java Edition · port 25565");javaToggle.setTextColor(ink);javaToggle.setTextSize(16);javaToggle.setMinHeight(dp(48));javaToggle.setChecked(getPreferences(0).getBoolean("java",true));card.addView(javaToggle);
  javaTarget=new EditText(this);javaTarget.setSingleLine(true);javaTarget.setTextColor(ink);javaTarget.setTextSize(16);javaTarget.setInputType(android.text.InputType.TYPE_CLASS_TEXT|android.text.InputType.TYPE_TEXT_VARIATION_URI);javaTarget.setText(getPreferences(0).getString("javaTarget","198.51.100.10:25565"));card.addView(javaTarget);
  LinearLayout choices=new LinearLayout(this);Button pub=button("Public domain"),local=button("Home network");publicChoice=pub;localChoice=local;choices.addView(pub,new LinearLayout.LayoutParams(0,-2,1));choices.addView(local,new LinearLayout.LayoutParams(0,-2,1));card.addView(choices);
  pub.setOnClickListener(v->{target.setText("198.51.100.10:19132");javaTarget.setText("198.51.100.10:25565");});local.setOnClickListener(v->{target.setText("192.0.2.10:19132");javaTarget.setText("192.0.2.10:25565");});
  state=text(ConnectorService.status,16,green);content.addView(state);
  start=button("Start LAN broadcast");start.setTextColor(Color.rgb(12,25,21));start.setBackgroundTintList(android.content.res.ColorStateList.valueOf(green));content.addView(start,new LinearLayout.LayoutParams(-1,dp(56)));
  stop=button("Stop broadcast");content.addView(stop,new LinearLayout.LayoutParams(-1,dp(52)));
  start.setOnClickListener(v->startConnector());stop.setOnClickListener(v->{ConnectorService.status="Stopping…";stopService(new Intent(this,ConnectorService.class));});
  content.addView(text("Bedrock: join “Minecraft LAN Broadcaster - Phone”. The real server handles world loading.\n\nJava: open Multiplayer and join “Minecraft LAN Broadcaster - Java”. Keep this app running while Java players are connected.\n\nKeep the phone and players on the same local network.",14,muted));
  content.addView(text("Private by design · No ads · No sign-in · No root",12,green));
  Button details=button("Show connection details");content.addView(details);
  logs=text("Connection events will appear here.",11,muted);logs.setTypeface(Typeface.MONOSPACE);logs.setTextIsSelectable(true);logs.setVisibility(View.GONE);content.addView(logs);
  details.setOnClickListener(v->{boolean show=logs.getVisibility()!=View.VISIBLE;logs.setVisibility(show?View.VISIBLE:View.GONE);details.setText(show?"Hide connection details":"Show connection details");});
  // ADB smoke-test entry point is limited to this debuggable test build.
  if ((getApplicationInfo().flags & android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE)!=0 && getIntent().getBooleanExtra("smokeStart",false)) {
   String t=getIntent().getStringExtra("target");if(t!=null)target.setText(t);String j=getIntent().getStringExtra("javaTarget");if(j!=null)javaTarget.setText(j);bedrockToggle.setChecked(getIntent().getBooleanExtra("bedrock",true));javaToggle.setChecked(getIntent().getBooleanExtra("java",true));handler.postDelayed(this::startConnector,500);
  }
 }
 private void startConnector(){
  if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS},1);
  boolean bedrock=bedrockToggle.isChecked(),java=javaToggle.isChecked();
  if(!bedrock&&!java){Toast.makeText(this,"Select at least one edition",Toast.LENGTH_SHORT).show();return;}
  String t=target.getText().toString().trim(),j=javaTarget.getText().toString().trim();getPreferences(0).edit().putString("target",t).putString("javaTarget",j).putBoolean("bedrock",bedrock).putBoolean("java",java).apply();
  ConnectorService.status="Starting…";
  startForegroundService(new Intent(this,ConnectorService.class).putExtra("target",t).putExtra("javaTarget",j).putExtra("bedrock",bedrock).putExtra("java",java));
 }
 private final Runnable refresh=new Runnable(){public void run(){
  state.setText(ConnectorService.status);boolean running=Mobile.isRunning();start.setEnabled(!running);stop.setEnabled(running);target.setEnabled(!running&&bedrockToggle.isChecked());javaTarget.setEnabled(!running&&javaToggle.isChecked());bedrockToggle.setEnabled(!running);javaToggle.setEnabled(!running);publicChoice.setEnabled(!running);localChoice.setEnabled(!running);
  start.setText(running?"Broadcast is running":"Start LAN broadcast");start.setAlpha(running?.45f:1f);stop.setAlpha(running?1f:.45f);
  String s=Mobile.logs();if(!s.isEmpty()&&!s.contentEquals(logs.getText()))logs.setText(s);
  if(running)getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
  handler.postDelayed(this,800);
 }};
 @Override public void onResume(){super.onResume();handler.post(refresh);}
 @Override public void onPause(){handler.removeCallbacks(refresh);super.onPause();}
}
