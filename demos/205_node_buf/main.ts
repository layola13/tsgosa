import { randomBytes } from "crypto";

function main(): i32 {
  console.log(Buffer.concat(["ab", "cd"]).length, randomBytes(16).length);
  return 0;
}