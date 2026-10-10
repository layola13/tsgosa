function greet(n: string = "hi"): string { return n; }
function main(): i32 {
  console.log(greet());
  console.log(greet("yo"));
  return 0;
}
