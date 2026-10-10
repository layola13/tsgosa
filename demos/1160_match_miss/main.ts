function main(): i32 {
  console.log("abc".match(/[0-9]+/)[0] ?? "miss");
  console.log("abc".match(/[0-9]+/).length);
  return 0;
}
