function main(): i32 {
  const [a, b = "e"] = ["x"] as string[];
  console.log(a);
  console.log(b);
  return 0;
}
