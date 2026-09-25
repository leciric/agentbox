package dev.agentbox.remedy;

import android.app.Activity;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.graphics.Typeface;
import android.os.Bundle;
import android.util.Log;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import java.util.ArrayList;

/** Remedy's medication reminders: a list you add to, with an empty state. */
public class MainActivity extends Activity {
    private static final String TAG = "Remedy";

    private final ArrayList<String> medications = new ArrayList<>();
    private ArrayAdapter<String> adapter;
    private TextView empty;
    private SharedPreferences prefs;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        prefs = getSharedPreferences("medications", MODE_PRIVATE);
        String saved = prefs.getString("list", "");
        if (!saved.isEmpty()) {
            for (String name : saved.split("\n")) {
                medications.add(name);
            }
        }

        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.rgb(250, 247, 255));
        root.setPadding(dp(20), dp(56), dp(20), dp(20));

        TextView title = new TextView(this);
        title.setText("Medications");
        title.setTextSize(TypedValue.COMPLEX_UNIT_SP, 32);
        title.setTypeface(Typeface.DEFAULT_BOLD);
        title.setTextColor(Color.rgb(46, 16, 101));
        root.addView(title);

        TextView subtitle = new TextView(this);
        subtitle.setText("Remedy reminds you when it's time.");
        subtitle.setTextSize(TypedValue.COMPLEX_UNIT_SP, 16);
        subtitle.setTextColor(Color.rgb(113, 113, 122));
        root.addView(subtitle);

        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(0, dp(24), 0, dp(8));
        EditText input = new EditText(this);
        input.setHint("Add a medication");
        input.setSingleLine(true);
        input.setImeOptions(EditorInfo.IME_ACTION_DONE);
        input.setContentDescription("Medication name");
        row.addView(input, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        Button add = new Button(this);
        add.setText("Add");
        add.setContentDescription("Add medication");
        row.addView(add);
        root.addView(row);

        empty = new TextView(this);
        empty.setText("No medications yet.\nAdd one above.");
        empty.setGravity(Gravity.CENTER);
        empty.setTextSize(TypedValue.COMPLEX_UNIT_SP, 18);
        empty.setTextColor(Color.rgb(161, 161, 170));
        empty.setPadding(0, dp(96), 0, 0);
        root.addView(empty, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        ListView list = new ListView(this);
        adapter = new ArrayAdapter<>(this, android.R.layout.simple_list_item_1, medications);
        list.setAdapter(adapter);
        root.addView(list, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));

        add.setOnClickListener(v -> addMedication(input));
        input.setOnEditorActionListener((v, actionId, event) -> {
            if (actionId != EditorInfo.IME_ACTION_DONE) {
                return false;
            }
            addMedication(input);
            return true;
        });

        setContentView(root);
        refresh();
        Log.i(TAG, "Showing " + medications.size() + " medication(s)");
    }

    private void addMedication(EditText input) {
        String name = input.getText().toString().trim();
        if (name.isEmpty()) {
            return;
        }
        medications.add(name);
        prefs.edit().putString("list", String.join("\n", medications)).apply();
        input.setText("");
        refresh();
        Log.i(TAG, "Added medication: " + name);
    }

    private void refresh() {
        adapter.notifyDataSetChanged();
        empty.setVisibility(medications.isEmpty() ? View.VISIBLE : View.GONE);
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }
}
