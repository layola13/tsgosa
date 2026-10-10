function em(parts: string[], x: string): string {
  return x;
}
function main(): i32 {
  console.log(em`a${"b"}c`);
  return 0;
}
