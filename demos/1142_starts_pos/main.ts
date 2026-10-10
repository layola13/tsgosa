function main(): i32 {
  console.log("hello".startsWith("ell", 1) ? 1 : 0);
  console.log("hello".startsWith("hell", 1) ? 1 : 0);
  return 0;
}
