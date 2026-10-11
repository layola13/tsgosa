function main(): i32 {
  const [a = "d"] = [] satisfies string[];
  console.log(a);
  return 0;
}
