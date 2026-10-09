function main(): i32 {
  console.log("a1b2".replace(/[0-9]/g, "#"));
  console.log("aaa".replace(/a/g, "b"));
  return 0;
}
