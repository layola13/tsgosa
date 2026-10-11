function main(): i32 {
  const [a = "d"] = ["x"] as string[];
  console.log(a);
  return 0;
}
