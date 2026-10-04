function greet(): void {
  console.log("hi");
}
function main(): i32 {
  const f = () => console.log(1);
  f();
  const g = (s: string) => console.log(s);
  g("hey");
  greet();
  return 0;
}
