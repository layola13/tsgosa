function main(): i32 {
  console.log("b\na".match(/^a/m)[0] ?? "miss");
  return 0;
}
