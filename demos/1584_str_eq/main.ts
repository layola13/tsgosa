function main(): i32 {
  const s1: string = "foo";
  const s2: string = "bar";
  console.log(s1 == s2 ? 1 : 0);
  console.log(s1 == "foo" ? 1 : 0);
  return 0;
}
