function main(): i32 {
  console.log("abc".startsWith("b", 1));
  console.log("abc".startsWith("b", 2));
  console.log("abc".startsWith("a", -1));
  console.log("abc".startsWith("c", 9));
  const p = 1;
  console.log("abc".startsWith("b", p));
  return 0;
}
