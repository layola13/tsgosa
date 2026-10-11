function main(): i32 {
  const sa: string[] = ["x"];
  const [a, b = "d"] = sa;
  console.log(a);
  console.log(b);
  return 0;
}
