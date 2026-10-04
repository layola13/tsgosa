import { normalize, dirname, extname, isAbsolute, join } from "path";

function main(): i32 {
  console.log(normalize("/a//b/../c"), dirname("/a/b/c.txt"), extname("c.txt"));
  console.log(isAbsolute("/a"), isAbsolute("a"));
  console.log(join("a", "b", "c"));
  return 0;
}