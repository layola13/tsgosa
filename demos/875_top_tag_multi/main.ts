function shout(parts: string[]): string {
  return parts[0];
}
function greet(parts: string[], name: string): string {
  return name;
}
function main(): i32 {
  console.log(shout`hi`);
  console.log(greet`yo${"bo"}`);
  return 0;
}
