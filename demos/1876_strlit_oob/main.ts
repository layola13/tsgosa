function main(): i32 {
  const [a, b = "d"] = ["x"];
  console.log(a);
  console.log(b);
  return 0;
}
