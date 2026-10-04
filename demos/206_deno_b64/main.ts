function main(): i32 {
  const e: string = btoa("hi");
  const d: string = atob(e);
  console.log(e.length, d.length);
  return 0;
}