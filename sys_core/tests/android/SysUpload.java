package com.sunyuanling.filesync.core;
import org.json.*;
import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.util.*;

/** Standalone ordinary-shell JNI/HTTP test. Does not load or read the installed app. */
public final class SysUpload {
    static native int nativeBridgeVersion();
    static native long nativeStart(String input);
    static native String nativePoll(long job);
    static native byte[] nativeTakeBody(long job,long request);
    static native void nativeReply(long job,long request,String result);
    static native void nativeCancel(long job);
    static String base;
    static void require(boolean value,String message) { if(!value) throw new AssertionError(message); }
    static byte[] read(InputStream in) throws Exception {
        ByteArrayOutputStream out=new ByteArrayOutputStream(); byte[] buf=new byte[65536]; int n;
        while((n=in.read(buf))!=-1) out.write(buf,0,n); return out.toByteArray();
    }
    static JSONObject request(long job,JSONObject event) throws Exception {
        if(event.getString("type").equals("probe_share")) {
            HttpURLConnection c=(HttpURLConnection)new URL(event.getString("url")).openConnection(); c.setConnectTimeout(10000); c.setReadTimeout(10000); c.setRequestProperty("Range","bytes=0-0");
            try { require(c.getResponseCode()==206,"range status"); require(c.getHeaderField("Content-Disposition").contains("attachment"),"attachment"); require(c.getHeaderField("Content-Range").startsWith("bytes 0-0/"),"range header"); require(c.getInputStream().read()>=0,"range byte"); return new JSONObject().put("code",200).put("message",""); }
            finally {c.disconnect();}
        }
        StringBuilder address=new StringBuilder(event.getString("base_url")+"/v1"+event.getString("path"));
        JSONArray query=event.getJSONArray("query");
        for(int i=0;i<query.length();i++) {JSONArray pair=query.getJSONArray(i); address.append(i==0?'?':'&').append(URLEncoder.encode(pair.getString(0),"UTF-8")).append('=').append(URLEncoder.encode(pair.getString(1),"UTF-8"));}
        JSONObject body=event.getJSONObject("body"); boolean bytes=body.getString("kind").equals("bytes");
        byte[] data=bytes?nativeTakeBody(job,event.getLong("id")):body.getJSONObject("value").toString().getBytes("UTF-8");
        HttpURLConnection c=(HttpURLConnection)new URL(address.toString()).openConnection(); c.setConnectTimeout(10000); c.setReadTimeout((int)event.getLong("timeout_ms")); c.setRequestMethod("POST"); c.setDoOutput(true); c.setFixedLengthStreamingMode(data.length); c.setRequestProperty("Content-Type",bytes?"application/octet-stream":"application/json");
        try {try(OutputStream output=c.getOutputStream()){output.write(data);} int status=c.getResponseCode(); if(status!=200) throw new IOException("HTTP "+status); return new JSONObject(new String(read(c.getInputStream()),"UTF-8"));}
        finally {c.disconnect();}
    }
    static void run(File file,String mode,boolean fixed,boolean quick) throws Exception {
        JSONObject input=new JSONObject().put("file",file.getAbsolutePath()).put("remote_dir","/fixture").put("base_url",base).put("device_id","fixture-device").put("quick_share",quick).put("main_node","active");
        if(fixed) input.put("chunk_size",512*1024).put("concurrency",2);
        input.put("nodes",new JSONArray().put(new JSONObject().put("id","active").put("url",base)));
        long job=nativeStart(input.toString()); require(job>0,"job handle"); long deadline=System.nanoTime()+90_000_000_000L; int requests=0,progress=0;
        try {
            while(System.nanoTime()<deadline) {
                JSONArray events=new JSONArray(nativePoll(job));
                for(int i=0;i<events.length();i++) {
                    JSONObject event=events.getJSONObject(i); String type=event.getString("type");
                    if(type.equals("request")||type.equals("probe_share")) {
                        requests++; JSONObject result;
                        try {result=new JSONObject().put("response",request(job,event));}
                        catch(Exception e) {result=new JSONObject().put("error",e.toString());}
                        nativeReply(job,event.getLong("id"),result.toString());
                    } else if(type.equals("failed")) throw new AssertionError(event.toString());
                    else if(type.equals("progress")) {require(event.getLong("sent")<=event.getLong("total"),"progress range");progress++;}
                    else if(type.equals("done")) {
                        JSONObject data=event.getJSONObject("data"); require(data.getLong("file_size")==file.length(),"completed size");
                        if(quick) require(data.getString("entry_url").equals(base),"verified share endpoint");
                        System.out.println(new JSONObject().put("test",mode).put("result","PASS").put("requests",requests).put("progress_events",progress).put("bytes",file.length())); return;
                    }
                }
                Thread.sleep(2);
            }
            throw new AssertionError("upload deadline");
        } finally {nativeCancel(job);}
    }
    public static void main(String[] args) throws Exception {
        File directory=new File(args[0]); base=args[1]; System.load(new File(directory,"libsync_core_jni.so").getAbsolutePath());
        if(args.length>2 && args[2].equals("hash-only")) {
            require(SyncCore.nativeAbiVersion()==4,"legacy ABI");
            require(SyncCore.nativeHashChunk(new byte[]{1,2,3}).length==32,"legacy hash");
            System.out.println("HASH_ONLY_PASS"); return;
        }
        require(nativeBridgeVersion()==1,"bridge version"); require(SyncCore.nativeAbiVersion()==4,"C ABI version");
        byte[] data=new byte[5*1024*1024+13]; for(int i=0;i<data.length;i++)data[i]=(byte)((i*17+3)%251);
        File source=new File(directory,"test-adaptive.bin"); try(FileOutputStream out=new FileOutputStream(source)){out.write(data);}
        byte[] packed=SyncCore.nativeDescribeFile(source.getAbsolutePath(),4*1024*1024); require(packed!=null&&packed.length==128,"JNI descriptor leaves");
        require(Arrays.equals(Arrays.copyOfRange(packed,0,32),SyncCore.nativeHashChunk(data)),"file hash matches JNI descriptor");
        require(Arrays.equals(Arrays.copyOfRange(packed,32,64),SyncCore.nativeMerkleRoot(Arrays.copyOfRange(packed,64,packed.length))),"Merkle descriptor matches JNI");
        System.out.println("JNI_ABI_HASH_MERKLE PASS");
        run(source,"adaptive",false,false);
        File fixed=new File(directory,"test-fixed.bin"); Files.copy(source.toPath(),fixed.toPath(),StandardCopyOption.REPLACE_EXISTING); run(fixed,"fixed_with_session_404",true,false);
        File quick=new File(directory,"test-quick.bin"); Files.copy(source.toPath(),quick.toPath(),StandardCopyOption.REPLACE_EXISTING); run(quick,"quick_share_range_verified",false,true);
        File empty=new File(directory,"test-empty.bin"); try(FileOutputStream out=new FileOutputStream(empty)){} run(empty,"empty_file",true,false);
        long cancelled=nativeStart(new JSONObject().put("file",source.getAbsolutePath()).put("remote_dir","/fixture").put("base_url",base).put("chunk_size",524288).toString()); nativeCancel(cancelled);
        boolean rejected=false;try{nativePoll(cancelled);}catch(IllegalStateException expected){rejected=true;}require(rejected,"cancelled handle");System.out.println("JNI_CANCEL PASS");
        for(File file:new File[]{source,fixed,quick,empty})require(file.delete(),"temporary source cleanup");
        System.out.println("ALL_ANDROID_JNI_ASSERTIONS_PASSED");
    }
}
