import { stat } from "node:fs/promises";
import { join } from "node:path";

const binary = join(process.cwd(), "bin", "gate");
const info = await stat(binary).catch((error) => {
  throw new Error(`missing packaged gate binary at ${binary}`, { cause: error });
});

if (!info.isFile()) {
  throw new Error(`packaged gate binary is not a file: ${binary}`);
}

if ((info.mode & 0o111) === 0) {
  throw new Error(`packaged gate binary is not executable: ${binary}`);
}
