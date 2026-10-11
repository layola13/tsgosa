function main(): i32 {
  const [a] = ["x"] satisfies string[];
  console.log(a);
  return 0;
}
