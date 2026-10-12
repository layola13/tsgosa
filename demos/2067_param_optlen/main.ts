function l(t: string|null): number {
  return t?.length ?? -1;
}
function main(): i32 {
  console.log(l("hey"));
  console.log(l(null));
  return 0;
}
