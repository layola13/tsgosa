function main(): i32 {
  console.log("hello".endsWith("lo", 5) ? 1 : 0);
  console.log("hello".endsWith("hell", 4) ? 1 : 0);
  console.log("hello".endsWith("lo", 4) ? 1 : 0);
  console.log("hello".endsWith("o", 99) ? 1 : 0);
  console.log("hello".endsWith("h", -2) ? 1 : 0);
  console.log("hello".endsWith("", 2) ? 1 : 0);
  console.log("hello".endsWith("lo") ? 1 : 0);
  return 0;
}
