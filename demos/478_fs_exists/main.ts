import { existsSync } from "node:fs";
function main(): i32 {
  console.log(existsSync("demos/478_fs_exists/main.ts"));
  console.log(existsSync("demos/478_fs_exists/no-such-file.ts"));
  return 0;
}
