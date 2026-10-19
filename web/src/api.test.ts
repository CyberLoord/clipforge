import assert from "node:assert/strict";
import test from "node:test";
import { uploadVideo } from "./api.ts";

test("upload sends the selected mode before the streamed file", async () => {
  const previous = Object.getOwnPropertyDescriptor(
    globalThis,
    "XMLHttpRequest",
  );
  let submitted: FormData | undefined;
  class UploadRequest {
    upload = {};
    responseType = "";
    status = 202;
    response = { id: "job", mode: "montage" };
    onload?: () => void;
    open(method: string, url: string) {
      assert.equal(method, "POST");
      assert.equal(url, "/api/jobs");
    }
    send(body: FormData) {
      submitted = body;
      this.onload?.();
    }
    abort() {}
  }
  Object.defineProperty(globalThis, "XMLHttpRequest", {
    configurable: true,
    value: UploadRequest,
  });
  try {
    const file = new File(["video"], "short.mp4", { type: "video/mp4" });
    await uploadVideo(file, "montage", () => {}).promise;
    assert.deepEqual([...submitted!.keys()], ["mode", "file"]);
    assert.equal(submitted!.get("mode"), "montage");
    assert.equal((submitted!.get("file") as File).name, "short.mp4");
  } finally {
    if (previous) Object.defineProperty(globalThis, "XMLHttpRequest", previous);
    else Reflect.deleteProperty(globalThis, "XMLHttpRequest");
  }
});
