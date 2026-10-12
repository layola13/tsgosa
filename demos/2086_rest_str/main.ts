function main(): i32 {
  const sa: string[] = ["a", "b", "c"];
  const [c, ...rest] = sa;
  console.log(c);
  console.log(rest[0]);
  console.log(rest[1]);
  return 0;
}
