function main(): i32 {
  const [a = "d"] = [] as readonly string[];
  console.log(a);
  return 0;
}
