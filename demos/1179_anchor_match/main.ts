function main(): i32 {
  console.log("abc".match(/^a/)[0] ?? "miss");
  console.log("abc".match(/c$/)[0] ?? "miss");
  return 0;
}
