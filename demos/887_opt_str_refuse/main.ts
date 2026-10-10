function a(): i32 {
  const s: string|null = "hi";
  const t = s?.foo;
  console.log(t);
  return 0;
}
function b(): i32 {
  const s: string|null = "hi";
  console.log(s?.toUpperCase());
  return 0;
}
function main(): i32 {
  return a() + b();
}
