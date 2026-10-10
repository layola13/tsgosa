function greet(n: string): string { return `hi ${n}`; }
function main(): i32 {
  console.log(greet("bo"));
  console.log(`${greet("a")}!`);
  return 0;
}
