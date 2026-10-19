package com.sunyuanling.filesync;

import android.app.Activity;
import android.content.ClipData;
import android.net.Uri;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.FrameLayout;
import java.io.File;
import java.io.FileOutputStream;

/** 独立测试进程只使用平台 Java API，不依赖目标应用提供的 Kotlin 运行库。 */
public class DragSourceActivity extends Activity {
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        try (FileOutputStream out = new FileOutputStream(new File(getCacheDir(), "drag-source.txt"))) {
            out.write("跨应用拖放快传测试内容".getBytes(java.nio.charset.StandardCharsets.UTF_8));
        } catch (java.io.IOException error) { throw new RuntimeException(error); }
        Button button = new Button(this);
        button.setText("长按拖放测试文件");
        button.setContentDescription("拖放测试源");
        button.setOnLongClickListener(view -> view.startDragAndDrop(
            ClipData.newUri(getContentResolver(), "拖放文件", Uri.parse("content://com.sunyuanling.filesync.test.dragfiles/drag-source.txt")),
            new View.DragShadowBuilder(view), null, View.DRAG_FLAG_GLOBAL | View.DRAG_FLAG_GLOBAL_URI_READ));
        FrameLayout frame = new FrameLayout(this);
        FrameLayout.LayoutParams params = new FrameLayout.LayoutParams(600, 180, Gravity.TOP | Gravity.CENTER_HORIZONTAL);
        params.topMargin = 1000;
        frame.addView(button, params);
        setContentView(frame);
    }
}
