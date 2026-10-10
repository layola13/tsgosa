function count(parts: string[]): i32 {
  return parts.length;
}
function bad(x: i32): string {
  return "z";
}
function shout(parts: string[]): string {
  return parts[0];
}
function a(): i32 {
  const s: string = count`ab`;
  return 0;
}
function b(): i32 {
  console.log(bad`hi`);
  return 0;
}
function c(): i32 {
  const n: i32 = shout`hi`;
  console.log(n);
  return 0;
}
function main(): i32 {
  return a() + b() + c();
}
